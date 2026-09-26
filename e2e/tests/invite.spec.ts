import { expect, test } from "@playwright/test";

import { createRoom, signUp } from "../support/app";

test("a friend joins a private room by invite link @cross", async ({ browser }) => {
  const ownerCtx = await browser.newContext();
  const friendCtx = await browser.newContext();
  const owner = await ownerCtx.newPage();
  const friend = await friendCtx.newPage();

  await signUp(owner, "host");
  const slug = await createRoom(owner, { name: "Private night", visibility: "private" });
  const res = await owner.request.post(`/api/v1/rooms/${slug}/invite-links`, { data: { expiresIn: "1d", maxUses: 1 } });
  expect(res.status()).toBe(201);
  const { url } = (await res.json()) as { url: string };
  await owner.goto(`/r/${slug}`);

  const friendName = await signUp(friend, "friend");
  // Before the link the room is closed to them.
  const denied = await friend.request.get(`/api/v1/rooms/${slug}`);
  expect(denied.status()).toBe(403);

  await friend.goto(new URL(url).pathname);
  await friend.getByRole("button", { name: /Join/ }).click();
  await expect(friend).toHaveURL(new RegExp(`/r/${slug}$`));
  await expect(friend.getByRole("heading", { name: "Private night" })).toBeVisible();

  // The owner sees them arrive in the room log.
  await expect(owner.locator(".chat-line.system", { hasText: friendName })).toBeVisible();

  await ownerCtx.close();
  await friendCtx.close();
});
