import { expect, test, type Browser, type Page } from "@playwright/test";

import { chat, createRoom, joinAsMember, signUp } from "../support/app";

async function two(browser: Browser) {
  const ownerCtx = await browser.newContext();
  const guestCtx = await browser.newContext();
  const owner = await ownerCtx.newPage();
  const guest = await guestCtx.newPage();
  return { owner, guest, close: async () => (await ownerCtx.close(), await guestCtx.close()) };
}

const options = (p: Page) => p.locator("summary", { hasText: "Options" });

test("the owner edits the room: name, description, privacy", async ({ browser }) => {
  const { owner, guest, close } = await two(browser);
  await signUp(owner, "editor");
  const slug = await createRoom(owner, { name: "Before" });
  await signUp(guest, "outsider");
  await guest.goto(`/r/${slug}`);
  await expect(guest.getByRole("heading", { name: "Before" })).toBeVisible();

  await owner.goto(`/r/${slug}/settings`);
  await owner.getByLabel("Name").fill("After");
  await owner.getByLabel(/Description/).fill("Fridays at nine");
  await owner.getByLabel("Private — invite only").check();
  await owner.getByRole("button", { name: "Save" }).click();
  await expect(owner.getByText(/saved/i).first()).toBeVisible();

  await owner.goto(`/r/${slug}`);
  await expect(owner.getByRole("heading", { name: "After" })).toBeVisible();
  await expect(owner.getByText("Fridays at nine")).toBeVisible();
  // Private now: the outsider cannot load it any more.
  const res = await guest.request.get(`/api/v1/rooms/${slug}`);
  expect(res.status()).toBe(403);
  await close();
});

test("roles, mutes and bans apply to a member in the room", async ({ browser }) => {
  const { owner, guest, close } = await two(browser);
  await signUp(owner, "chief");
  const slug = await createRoom(owner);
  const name = await signUp(guest, "member");
  await joinAsMember(owner, guest, slug);

  // Promoted: the member gets the moderator tools without reloading.
  await owner.goto(`/r/${slug}/settings`);
  const row = owner.locator("tr", { hasText: name });
  await row.getByRole("button", { name: "Make moderator" }).click();
  await expect(row).toContainText("moderator");
  await guest.reload();
  await expect(guest.getByRole("link", { name: "Settings" })).toBeVisible();
  await row.getByRole("button", { name: "Demote" }).click();
  await expect(row.locator(".badge").first()).toHaveText("member");

  // Muted members cannot chat until unmuted.
  await row.getByLabel(`Mute ${name}`).selectOption("5");
  await expect(row).toContainText("muted until");
  await guest.goto(`/r/${slug}`);
  await guest.getByPlaceholder("Say something").fill("can anyone hear me");
  await guest.getByRole("button", { name: "Send" }).click();
  await expect(guest.getByText(/you are muted until/)).toBeVisible();
  await row.getByRole("button", { name: "Unmute" }).click();
  await chat(guest, "back again");

  // A ban ends their session in the room at once.
  await row.getByRole("button", { name: "Ban" }).click();
  await expect(guest.getByRole("alert").filter({ hasText: "You have been banned" })).toBeVisible();
  await guest.goto(`/r/${slug}`);
  await expect(guest.getByText(/banned/i).first()).toBeVisible();
  await close();
});

test("moderating the chat: delete, slow mode, clear", async ({ browser }) => {
  const { owner, guest, close } = await two(browser);
  await signUp(owner, "mod");
  const slug = await createRoom(owner);
  await signUp(guest, "talker");
  await owner.goto(`/r/${slug}`);
  await guest.goto(`/r/${slug}`);

  await chat(guest, "buy cheap stuff");
  const spam = owner.locator(".chat-line", { hasText: "buy cheap stuff" });
  await spam.hover();
  await spam.getByRole("button", { name: "Delete" }).click();
  await expect(guest.locator(".chat-line", { hasText: "buy cheap stuff" })).toHaveCount(0);

  await options(owner).click();
  await owner.getByLabel("Slow mode").selectOption("30");
  await chat(guest, "first");
  await guest.getByPlaceholder("Say something").fill("second");
  await guest.getByRole("button", { name: "Send" }).click();
  await expect(guest.getByText(/slow mode: wait \d+ s/)).toBeVisible();
  await owner.getByLabel("Slow mode").selectOption("0");

  owner.once("dialog", (d) => void d.accept());
  await owner.getByRole("button", { name: "Clear chat" }).click();
  await expect(guest.locator(".chat-line:not(.system)")).toHaveCount(0);
  await expect(guest.locator(".chat-line.system", { hasText: "cleared the chat" })).toBeVisible();
  await close();
});

test("ending the session tells viewers why, and they stay out of the reconnect loop", async ({ browser }) => {
  const { owner, guest, close } = await two(browser);
  await signUp(owner, "closer");
  const slug = await createRoom(owner);
  await signUp(guest, "viewer");
  await owner.goto(`/r/${slug}`);
  await guest.goto(`/r/${slug}`);
  await expect(owner.locator(".chat-line.system", { hasText: "joined" }).last()).toBeVisible();

  await options(owner).click();
  owner.once("dialog", (d) => void d.accept());
  await owner.getByRole("button", { name: "End session" }).click();
  const notice = guest.getByRole("alert").filter({ hasText: "Session ended" });
  await expect(notice).toBeVisible();
  await expect(notice).toContainText("A moderator closed the session");
  // Nothing can be typed into a closed session.
  await expect(guest.getByPlaceholder("Say something")).toHaveCount(0);
  await expect(guest.getByPlaceholder("Paste a YouTube or video link")).toHaveCount(0);
  // No "reconnecting" banner comes back.
  await guest.waitForTimeout(1500);
  await expect(guest.getByText(/Reconnecting/)).toHaveCount(0);
  await close();
});

test("deleting a room sends everyone away", async ({ browser }) => {
  const { owner, guest, close } = await two(browser);
  await signUp(owner, "destroyer");
  const slug = await createRoom(owner, { name: "Short lived" });
  await signUp(guest, "bystander");
  await guest.goto(`/r/${slug}`);
  await expect(guest.getByRole("heading", { name: "Short lived" })).toBeVisible();

  await owner.goto(`/r/${slug}/settings`);
  owner.once("dialog", (d) => void d.accept());
  await owner.getByRole("button", { name: "Delete room" }).click();
  await expect(owner).toHaveURL(/\/$/);
  await expect(guest.getByRole("alert").filter({ hasText: "This room is gone" })).toBeVisible();
  expect((await owner.request.get(`/api/v1/rooms/${slug}`)).status()).toBe(404);
  await close();
});
