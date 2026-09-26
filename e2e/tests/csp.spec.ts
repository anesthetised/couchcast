import { expect, test, type Page } from "@playwright/test";

import { chat, createRoom, makeAdmin, PLAYABLE_TITLE, PLAYABLE_URL, signUp } from "../support/app";

// The Content-Security-Policy comes from the Go server, so this only means
// something against the built bundle (`E2E_BUNDLE=1 just e2e`, as in CI);
// the Vite dev server sends no policy and the test skips.

test.describe.configure({ timeout: 90_000 });

async function shownSeconds(page: Page): Promise<number> {
  const text = (await page.locator(".transport .time").first().textContent()) ?? "0:00";
  const [m, s] = text.trim().split(":").map(Number);
  return m! * 60 + s!;
}

test("the app runs under its Content-Security-Policy @cross @media", async ({ browser }) => {
  const ctx = await browser.newContext();
  const violations: string[] = [];
  await ctx.exposeBinding("__cspViolation", (_source, v: string) => void violations.push(v));
  await ctx.addInitScript(() => {
    document.addEventListener("securitypolicyviolation", (e) => {
      (window as unknown as { __cspViolation: (v: string) => void }).__cspViolation(`${e.effectiveDirective} ${e.blockedURI} on ${location.pathname}`);
    });
  });
  const page = await ctx.newPage();

  const res = await page.goto("/");
  const policy = res?.headers()["content-security-policy"];
  test.skip(!policy, "served by Vite, which sends no policy");
  expect(res?.headers()["x-frame-options"]).toBe("DENY");

  const name = await signUp(page, "strict");
  await makeAdmin(name);

  // A room playing a real clip: Shaka, MediaSource blobs, the socket,
  // thumbnails, chat and the link card.
  const slug = await createRoom(page, { firstUrl: PLAYABLE_URL });
  await page.goto(`/r/${slug}`);
  await expect(page.locator(".queue-item.current")).toContainText(PLAYABLE_TITLE);
  // Soft: when the policy blocks playback, the report should also list
  // the violations below.
  await expect.soft.poll(() => shownSeconds(page), { timeout: 30_000 }).toBeGreaterThanOrEqual(2);
  await chat(page, `look ${PLAYABLE_URL}`);

  for (const path of ["/", "/new", "/me", `/r/${slug}/settings`, "/admin", "/admin?tab=bugs", "/admin?tab=storage"]) {
    await page.goto(path);
    await expect(page.locator("main")).toBeVisible();
    await page.waitForLoadState("networkidle");
  }

  expect(violations).toEqual([]);
  await ctx.close();
});
