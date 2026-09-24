import { expect, test } from "@playwright/test";

import { createRoom, readyVideo, signUp } from "../support/app";

test("the room waits for a viewer who is buffering", async ({ page }) => {
  const name = await signUp(page, "waiter");
  const slug = await createRoom(page, { firstUrl: await readyVideo("Long film") });
  await page.goto(`/r/${slug}`);
  await expect(page.locator(".queue-item.current")).toContainText("Long film");

  // A second connection of the same user reports a stall, as a slow
  // device would; after a few seconds the room pauses for it.
  await page.evaluate(async (room) => {
    const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/api/v1/rooms/${room}/ws`);
    await new Promise((resolve) => ws.addEventListener("open", resolve, { once: true }));
    ws.send(JSON.stringify({ type: "report", state: "buffering", positionMs: 0 }));
    (window as unknown as { stalled: WebSocket }).stalled = ws;
  }, slug);

  const overlay = page.locator(".video-overlay.waiting");
  await expect(overlay).toContainText(`Waiting for ${name}`, { timeout: 10_000 });
  await expect(page.locator(".chat-line.system", { hasText: `waiting for ${name}` })).toBeVisible();

  // Ready again: the overlay goes and the room plays on.
  await page.evaluate(() => {
    (window as unknown as { stalled: WebSocket }).stalled.send(JSON.stringify({ type: "report", state: "playing", positionMs: 0 }));
  });
  await expect(overlay).toHaveCount(0);
});

test("moderators see who lags behind", async ({ page }) => {
  const name = await signUp(page, "laggy");
  const slug = await createRoom(page, { firstUrl: await readyVideo("Lag test") });
  await page.goto(`/r/${slug}`);
  await expect(page.locator(".queue-item.current")).toContainText("Lag test");

  // A second tab of the same user reports a position 7 s behind the clock.
  await page.evaluate(async (room) => {
    const ws = new WebSocket(`${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/api/v1/rooms/${room}/ws`);
    await new Promise((resolve) => ws.addEventListener("open", resolve, { once: true }));
    await new Promise((r) => setTimeout(r, 8000));
    ws.send(JSON.stringify({ type: "report", state: "playing", positionMs: 1000 }));
    (window as unknown as { lagging: WebSocket }).lagging = ws;
  }, slug);

  const avatar = page.locator(".presence .avatar.lagging");
  await expect(avatar).toHaveCount(1);
  await expect(avatar).toHaveAttribute("title", new RegExp(`${name} .*s behind`));
});

test("a start with countdown shows 3-2-1 to everyone", async ({ browser }) => {
  const hostCtx = await browser.newContext();
  const guestCtx = await browser.newContext();
  const host = await hostCtx.newPage();
  const guest = await guestCtx.newPage();
  await signUp(host, "starter");
  const slug = await createRoom(host, { firstUrl: await readyVideo("Premiere") });
  await host.goto(`/r/${slug}`);
  await expect(host.locator(".queue-item.current")).toContainText("Premiere");
  await signUp(guest, "audience");
  await guest.goto(`/r/${slug}`);

  const playButton = (p: typeof host) => p.locator(".controls .transport button.icon").first();
  await playButton(host).click(); // pause
  await expect(playButton(host)).toHaveText("▶");

  await host.locator("summary", { hasText: "Options" }).click();
  await host.getByRole("button", { name: "Start with countdown" }).click();
  for (const p of [host, guest]) await expect(p.locator(".countdown-number")).toBeVisible();
  for (const p of [host, guest]) await expect(p.locator(".countdown-number")).toHaveCount(0, { timeout: 6_000 });
  for (const p of [host, guest]) await expect(playButton(p)).toHaveText("❚❚");

  await hostCtx.close();
  await guestCtx.close();
});
