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

async function settle(page: Page, path: string) {
  await page.goto(path);
  await page.waitForLoadState("networkidle");
}

async function violations(page: Page) {
  const result = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"]).analyze();
  return result.violations
    .filter((v) => v.impact === "serious" || v.impact === "critical")
    .map((v) => `${v.id} (${v.impact}): ${v.help}\n    ${v.nodes.slice(0, 3).map((n) => `${n.target.join(" ")} — ${(n.failureSummary ?? "").split("\n").slice(1, 2).join(" ").trim()}`).join("\n    ")}`);
}

test("the main pages have no serious accessibility problems", async ({ page }) => {
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

test("the room's dialogs have no serious accessibility problems", async ({ page }) => {
  await signUp(page, "dialogs");
  const slug = await createRoom(page, { firstUrl: await readyVideo("Dialog video") });
  await settle(page, `/r/${slug}`);
  const found: string[] = [];

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

test.describe("at phone width", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test("no page scrolls sideways", async ({ page }) => {
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
