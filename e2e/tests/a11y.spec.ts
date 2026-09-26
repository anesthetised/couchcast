import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Page } from "@playwright/test";

import { chat, createRoom, makeAdmin, readyVideo, signUp } from "../support/app";

// pages lists what a signed-in owner (and admin) can open; each test
// visits them all with fresh data.
async function pages(page: Page): Promise<string[]> {
  const name = await signUp(page, "auditor");
  await makeAdmin(name);
  const slug = await createRoom(page, { name: "Audit room", firstUrl: await readyVideo("Audit video") });
  await page.goto(`/r/${slug}`);
  await chat(page, "hello @auditor, see 0:42 https://youtu.be/aqz-KE-bpKQ");
  return ["/", "/new", "/me", `/r/${slug}`, `/r/${slug}/settings`, "/admin", "/admin?tab=users", "/admin?tab=audit"];
}

// Anonymous pages are checked separately, logged out.
const PUBLIC = ["/", "/login", "/register"];

// settle waits for the app to render the page. Not "networkidle": the
// room's player keeps the network busy.
async function settle(page: Page, path: string) {
  await page.goto(path);
  await page.waitForFunction(() => (document.querySelector("#root")?.children.length ?? 0) > 0);
  await page.locator(".skeleton").first().waitFor({ state: "detached", timeout: 5_000 }).catch(() => {});
}

async function violations(page: Page) {
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "best-practice"]).analyze();
  return result.violations
    .filter((v) => v.impact === "serious" || v.impact === "critical" || v.impact === "moderate")
    .map((v) => `${v.id} (${v.impact}): ${v.help}\n    ${v.nodes.slice(0, 3).map((n) => `${n.target.join(" ")} — ${(n.failureSummary ?? "").split("\n").slice(1, 2).join(" ").trim()}`).join("\n    ")}`);
}

test("the main pages have no accessibility problems (moderate and up)", async ({ page }) => {
  test.setTimeout(120_000); // eleven pages, each scanned
  const found: string[] = [];
  for (const path of await pages(page)) {
    await settle(page, path);
    for (const v of await violations(page)) found.push(`${path}: ${v}`);
  }
  await page.context().clearCookies();
  for (const path of PUBLIC) {
    await settle(page, path);
    for (const v of await violations(page)) found.push(`${path} (anonymous): ${v}`);
  }
  expect(found, found.join("\n")).toEqual([]);
});

test("the room's dialogs have no accessibility problems (moderate and up)", async ({ page }) => {
  await signUp(page, "dialogs");
  const slug = await createRoom(page, { firstUrl: await readyVideo("Dialog video") });
  await settle(page, `/r/${slug}`);
  const found: string[] = [];
  // The player owns the hotkeys; wait until it is on the page.
  await expect(page.locator(".controls .transport button.icon").first()).toBeVisible();

  await page.locator("body").press("?");
  await expect(page.getByRole("dialog")).toBeVisible();
  for (const v of await violations(page)) found.push(`hotkeys: ${v}`);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await page.getByRole("button", { name: "Report a problem" }).click();
  await expect(page.getByRole("dialog", { name: "Report a problem" })).toBeVisible();
  for (const v of await violations(page)) found.push(`bug report: ${v}`);
  await page.keyboard.press("Escape");

  await page.locator(".queue-item.current").getByRole("button", { name: "Report" }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  for (const v of await violations(page)) found.push(`video report: ${v}`);

  expect(found, found.join("\n")).toEqual([]);
});

test("keyboard users can skip the header", async ({ page }) => {
  // A page without autofocus, freshly loaded (the room focuses its add
  // field when the queue is empty).
  await signUp(page, "keys");
  await settle(page, "/me");
  await page.keyboard.press("Tab");
  const skip = page.getByRole("link", { name: "Skip to content" });
  await expect(skip).toBeFocused();
  await expect(skip).toBeInViewport();
  await page.keyboard.press("Enter");
  await expect(page.locator("main#main")).toBeFocused();
  // The next stop is inside the page, not the header.
  await page.keyboard.press("Tab");
  expect(await page.evaluate(() => document.activeElement?.closest("main") !== null)).toBe(true);
});

test.describe("with reduced motion", () => {
  test.use({ reducedMotion: "reduce" });

  test("nothing animates, and reactions still show", async ({ page }) => {
    await signUp(page, "still");
    const slug = await createRoom(page, { firstUrl: await readyVideo("Calm video") });
    await settle(page, `/r/${slug}`);
    await expect(page.locator(".queue-item.current")).toContainText("Calm video"); // joined
    await page.evaluate(async (room) => {
      const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/api/v1/rooms/${room}/ws`);
      await new Promise((r) => ws.addEventListener("open", r, { once: true }));
      ws.send(JSON.stringify({ type: "react", emoji: "🔥" }));
    }, slug);
    await expect(page.locator(".reaction").first()).toBeVisible();
    // Fades are fine; nothing may move (no transform keyframes running).
    const moving = await page.evaluate(() =>
      document
        .getAnimations()
        .filter((a) => a.playState === "running")
        .filter((a) => (a.effect as KeyframeEffect | null)?.getKeyframes().some((k) => "transform" in k)).length,
    );
    expect(moving).toBe(0);
  });
});

test.describe("at phone width", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test("no page scrolls sideways", async ({ page }) => {
    test.setTimeout(120_000);
    const wide: string[] = [];
    const check = async (path: string) => {
      await settle(page, path);
      const [scroll, width] = await page.evaluate(() => [document.documentElement.scrollWidth, window.innerWidth]);
      if (scroll > width) wide.push(`${path}: ${scroll}px wide in a ${width}px viewport`);
    };
    for (const path of await pages(page)) await check(path);
    await page.context().clearCookies();
    for (const path of PUBLIC) await check(path);
    expect(wide, wide.join("\n")).toEqual([]);
  });

  test("the room is usable: chat, queue and controls fit", async ({ page }) => {
    await signUp(page, "pocket");
    const slug = await createRoom(page, { firstUrl: await readyVideo("Phone video") });
    await settle(page, `/r/${slug}`);
    const fits = async (el: ReturnType<Page["locator"]>) => {
      await el.scrollIntoViewIfNeeded();
      const box = await el.boundingBox();
      expect(box).not.toBeNull();
      expect(box!.x).toBeGreaterThanOrEqual(0);
      expect(box!.x + box!.width).toBeLessThanOrEqual(375);
    };
    // Chat and queue share the space below the player, one tab at a time.
    await fits(page.locator(".controls .transport button.icon").first());
    await fits(page.getByPlaceholder("Say something"));
    await chat(page, "typed on a phone");
    await page.getByRole("tab", { name: /Queue/ }).click();
    await expect(page).toHaveURL(/[?&]tab=queue/);
    await fits(page.getByPlaceholder("Paste a YouTube or video link"));
  });
});
