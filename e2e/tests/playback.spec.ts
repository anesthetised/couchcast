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
