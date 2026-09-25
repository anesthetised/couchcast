import { expect, test, type Page } from "@playwright/test";

import { createRoom, PLAYABLE_TITLE, PLAYABLE_URL, signUp } from "../support/app";

// The elapsed time the player shows, in seconds.
async function shownSeconds(page: Page): Promise<number> {
  const text = (await page.locator(".transport .time").first().textContent()) ?? "0:00";
  const [m, s] = text.trim().split(":").map(Number);
  return m! * 60 + s!;
}

const playButton = (p: Page) => p.locator(".controls .transport button.icon").first();

// gap is how far apart two players are, in whole seconds.
const gap = async (a: Page, b: Page) => Math.abs((await shownSeconds(a)) - (await shownSeconds(b)));

// settled resolves once the player's time stops moving.
async function settled(page: Page): Promise<number> {
  await expect
    .poll(async () => {
      const a = await shownSeconds(page);
      await page.waitForTimeout(700);
      return (await shownSeconds(page)) - a;
    })
    .toBe(0);
  return shownSeconds(page);
}

test("viewers follow the host: play, pause, seek and speed", async ({ browser }) => {
  const hostCtx = await browser.newContext();
  const guestCtx = await browser.newContext();
  const host = await hostCtx.newPage();
  const guest = await guestCtx.newPage();
  await signUp(host, "director");
  const slug = await createRoom(host, { firstUrl: PLAYABLE_URL });
  await host.goto(`/r/${slug}`);
  await expect(host.locator(".queue-item.current")).toContainText(PLAYABLE_TITLE);
  await signUp(guest, "audience");
  await guest.goto(`/r/${slug}`);

  // The clip really plays for both, on the same clock.
  await expect.poll(() => shownSeconds(guest), { timeout: 15_000 }).toBeGreaterThanOrEqual(2);
  await expect.poll(() => gap(host, guest)).toBeLessThanOrEqual(1);

  // Pause: the guest stops where the host stopped.
  await playButton(host).click();
  await expect(playButton(guest)).toHaveText("▶");
  const paused = await settled(guest);
  await expect.poll(() => gap(host, guest)).toBeLessThanOrEqual(1);

  // Seek (→ jumps ahead): the guest lands on the same second.
  await host.locator("body").press("ArrowRight");
  await expect.poll(() => shownSeconds(guest)).toBeGreaterThanOrEqual(paused + 4);
  await expect.poll(() => gap(host, guest)).toBeLessThanOrEqual(1);

  // Speed: everyone switches to 1.5× and the log says who did it.
  await host.getByLabel("Playback speed").selectOption("1.5");
  await expect(guest.getByLabel("Playback speed")).toHaveCount(0); // moderators only
  await expect(guest.locator(".chat-line.system", { hasText: "set speed to 1.5×" })).toBeVisible();

  // Play again: both move on together.
  await playButton(host).click();
  await expect(playButton(guest)).toHaveText("❚❚");
  const before = await shownSeconds(guest);
  await expect.poll(() => shownSeconds(guest)).toBeGreaterThan(before);
  await expect.poll(() => gap(host, guest)).toBeLessThanOrEqual(1);

  // Only moderators control playback.
  await expect(playButton(guest)).toBeDisabled();

  await hostCtx.close();
  await guestCtx.close();
});

test("a late joiner starts at the room's position", async ({ browser }) => {
  const hostCtx = await browser.newContext();
  const lateCtx = await browser.newContext();
  const host = await hostCtx.newPage();
  const late = await lateCtx.newPage();
  await signUp(host, "early");
  const slug = await createRoom(host, { firstUrl: PLAYABLE_URL });
  await host.goto(`/r/${slug}`);
  await expect.poll(() => shownSeconds(host), { timeout: 15_000 }).toBeGreaterThanOrEqual(5);

  await late.goto(`/r/${slug}`); // anonymous
  await expect.poll(() => shownSeconds(late), { timeout: 15_000 }).toBeGreaterThanOrEqual(5);
  await expect.poll(() => gap(host, late)).toBeLessThanOrEqual(1);

  await hostCtx.close();
  await lateCtx.close();
});
