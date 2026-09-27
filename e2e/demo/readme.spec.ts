import { writeFileSync } from "node:fs";
import { relative } from "node:path";

import { expect, test, type Browser, type Page } from "@playwright/test";

// Stages a movie night for the README (`just demo-media`): three people in
// a room playing an open movie, a queue and a chat. Writes the hero
// screenshot and the raw clips the recipe turns into a GIF.
//
// The movies are Blender open movies (CC BY); the queue also holds a
// famous music video, shown only as a title and thumbnail.

const PASSWORD = "demo-password-123";
const SINTEL = "https://www.youtube.com/watch?v=eRsGyueVLvQ";
const QUEUE = [
  "https://www.youtube.com/watch?v=aqz-KE-bpKQ", // Big Buck Bunny
  "https://www.youtube.com/watch?v=dQw4w9WgXcQ", // Never Gonna Give You Up
  "https://www.youtube.com/watch?v=WhWc3b3KhnY", // Spring
];
const OUT = "demo-results";

// person signs a user up (or in, on a rerun) in a window of their own.
async function person(browser: Browser, name: string, opts: { width: number; height: number }): Promise<Page> {
  const ctx = await browser.newContext({ viewport: { width: opts.width, height: opts.height }, deviceScaleFactor: 1.25 });
  let res = await ctx.request.post("/api/v1/auth/register", { data: { username: name, password: PASSWORD } });
  if (res.status() === 409) res = await ctx.request.post("/api/v1/auth/login", { data: { username: name, password: PASSWORD } });
  expect(res.ok(), await res.text()).toBe(true);
  return ctx.newPage();
}

// seconds shown by the player's clock.
async function shownSeconds(page: Page): Promise<number> {
  const text = (await page.locator(".transport .time").first().textContent()) ?? "0:00";
  return text
    .trim()
    .split(":")
    .map(Number)
    .reduce((acc, n) => acc * 60 + n, 0);
}

// seekTo moves the room as the seek bar does, without scrolling the page
// to it (which fill() would).
async function seekTo(page: Page, seconds: number) {
  await page.getByLabel("Position").evaluate((el: HTMLInputElement, ms) => {
    el.value = String(ms);
    el.dispatchEvent(new Event("change", { bubbles: true }));
  }, seconds * 1000);
  await expect.poll(() => shownSeconds(page)).toBeGreaterThanOrEqual(seconds);
  // Let the new position buffer and the frame settle.
  await expect(page.getByRole("status", { name: "Buffering" })).toHaveCount(0, { timeout: 60_000 });
  await page.waitForTimeout(1500);
}

async function say(page: Page, text: string) {
  await page.getByPlaceholder("Say something").fill(text);
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".chat-line", { hasText: text }).last()).toBeVisible();
}

test("movie night", async ({ browser }) => {
  // Tall enough for the queue under the player.
  const host = await person(browser, "maya", { width: 1440, height: 1200 });
  // A rerun starts from a fresh room; downloaded media stay cached.
  await host.request.delete("/api/v1/rooms/movie-night");
  const res = await host.request.post("/api/v1/rooms", {
    data: { name: "Friday movie night", slug: "movie-night", visibility: "public", description: "Open movies and questionable picks", firstUrl: SINTEL },
  });
  expect(res.status(), await res.text()).toBe(201);
  await host.goto("/r/movie-night");

  for (const url of QUEUE) {
    await host.getByPlaceholder("Paste a YouTube or video link").fill(url);
    await host.getByRole("button", { name: "Add", exact: true }).click();
    await expect(host.getByPlaceholder("Paste a YouTube or video link")).toHaveValue("");
  }
  await expect(host.locator(".queue-item")).toHaveCount(4, { timeout: 60_000 });

  // Downloading and packaging the movie takes a few minutes.
  await expect.poll(() => shownSeconds(host), { timeout: 15 * 60_000, intervals: [5000] }).toBeGreaterThanOrEqual(2);

  if (process.env.DEMO_EXPLORE) {
    for (const s of [95, 180, 250, 330, 400, 480, 560, 640, 700]) {
      await seekTo(host, s);
      await host.locator(".player video").screenshot({ path: `${OUT}/explore-${s}.jpg`, type: "jpeg", quality: 80 });
    }
    return;
  }

  // The hero shot: three people, the queue and a chat around a sunset.
  const theo = await person(browser, "theo", { width: 1280, height: 800 });
  const iris = await person(browser, "iris", { width: 1280, height: 800 });
  await theo.goto("/r/movie-night");
  await iris.goto("/r/movie-night");
  await say(theo, "popcorn's ready 🍿");
  await say(iris, "who queued Rick Astley? 👀");
  await say(host, "no idea 😇");
  // The typing hints fade a few seconds after the last message.
  await expect(host.getByText(/typing/)).toHaveCount(0, { timeout: 15_000 });
  await seekTo(host, 328);
  await host.screenshot({ path: `${OUT}/room.jpg`, type: "jpeg", quality: 85 });

  // The sync clip: the host above a guest; the guest's player follows
  // pause, seek and play, and a chat line reaches both. Wide enough for
  // the desktop layout, with the chat beside the player.
  await seekTo(host, 176);
  const size = { width: 1100, height: 700 };
  // A recording starts when its page opens.
  const left = await filmed(browser, "maya", { ...size, video: "clip-host" });
  const leftOpened = Date.now();
  const right = await filmed(browser, "theo", { ...size, video: "clip-guest" });
  const rightOpened = Date.now();
  await Promise.all([left.goto("/r/movie-night"), right.goto("/r/movie-night")]);
  await expect.poll(() => shownSeconds(right), { timeout: 60_000 }).toBeGreaterThanOrEqual(178);
  await right.waitForTimeout(1500);

  const from = Date.now();
  const play = left.locator(".controls .transport button.icon").first();
  await play.click(); // pause
  await expect(right.locator(".controls .transport button.icon").first()).toHaveText("▶");
  await right.waitForTimeout(1800);
  await seekTo(left, 330);
  await expect.poll(() => shownSeconds(right)).toBeGreaterThanOrEqual(330);
  await right.waitForTimeout(1200);
  await play.click(); // play
  await right.waitForTimeout(2000);
  await say(right, "perfectly in sync 👌");
  await right.waitForTimeout(1800);
  const to = Date.now();

  await left.context().close();
  await right.context().close();
  // For the recipe, which cuts both recordings to the same wall-clock
  // span: host, guest, where each span starts and how long it lasts.
  const rel = async (p: Page) => relative(OUT, await p.video()!.path());
  const args = [await rel(left), await rel(right), (from - leftOpened) / 1000, (from - rightOpened) / 1000, (to - from) / 1000];
  writeFileSync(`${OUT}/clip.txt`, args.join(" ") + "\n");
});

// filmed signs an existing user in, in a recorded window.
async function filmed(browser: Browser, name: string, opts: { width: number; height: number; video: string }): Promise<Page> {
  const ctx = await browser.newContext({
    viewport: { width: opts.width, height: opts.height },
    recordVideo: { dir: `${OUT}/${opts.video}`, size: { width: opts.width, height: opts.height } },
  });
  const res = await ctx.request.post("/api/v1/auth/login", { data: { username: name, password: PASSWORD } });
  expect(res.status(), await res.text()).toBe(200);
  return ctx.newPage();
}
