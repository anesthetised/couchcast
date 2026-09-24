import { expect, test } from "@playwright/test";

import { chat, createRoom, signUp, uniq } from "../support/app";

test("create a room from the form and land in it", async ({ page }) => {
  await signUp(page, "owner");
  const slug = uniq("new").replace(/_/g, "-").toLowerCase();
  await page.goto("/new");
  await page.getByLabel("Name").fill("Friday movies");
  await page.getByPlaceholder("friday", { exact: true }).fill(slug);
  await expect(page.locator("#slug-hint")).toHaveText("available");
  await page.getByRole("button", { name: "Create & open" }).click();
  await expect(page).toHaveURL(new RegExp(`/r/${slug}$`));
  await expect(page.getByRole("heading", { name: "Friday movies" })).toBeVisible();
});

test("chat: send, reply, pin, emoji", async ({ page }) => {
  await signUp(page, "chatty");
  const slug = await createRoom(page);
  await page.goto(`/r/${slug}`);

  await chat(page, "rules: be kind");
  // Match on the body: a reply also contains the text, in its quote.
  const rules = page.locator(".chat-line").filter({ has: page.locator(".chat-body", { hasText: /^rules: be kind$/ }) });

  // Reply quotes the original.
  await rules.hover();
  await rules.getByRole("button", { name: "reply", exact: true }).click();
  await expect(page.locator(".chat-replying")).toContainText("rules: be kind");
  await chat(page, "sure thing");
  await expect(page.locator(".chat-line", { hasText: "sure thing" }).locator(".chat-quote")).toContainText("rules: be kind");

  // The owner pins; the bar shows above the chat.
  await rules.hover();
  await rules.getByRole("button", { name: "pin", exact: true }).click();
  await expect(page.locator(".chat-pinned")).toContainText("rules: be kind");

  // :shortcode: autocomplete inserts the emoji.
  const input = page.getByPlaceholder("Say something");
  await input.fill("popcorn time :popc");
  await expect(page.locator(".chat-emoji-menu")).toBeVisible();
  await input.press("Tab");
  await expect(input).toHaveValue("popcorn time 🍿 ");
});

test("chat: edit one's own message, others see (edited)", async ({ browser }) => {
  const hostCtx = await browser.newContext();
  const guestCtx = await browser.newContext();
  const host = await hostCtx.newPage();
  const guest = await guestCtx.newPage();
  await signUp(host, "editor");
  const slug = await createRoom(host);
  await signUp(guest, "reader");
  await host.goto(`/r/${slug}`);
  await guest.goto(`/r/${slug}`);

  await chat(host, "see you at 9");
  const line = host.locator(".chat-line", { hasText: "see you at 9" });
  await line.hover();
  await line.getByRole("button", { name: "edit", exact: true }).click();
  const input = host.getByPlaceholder("Say something");
  await expect(input).toHaveValue("see you at 9");
  await expect(host.locator(".chat-replying")).toContainText("Editing");
  await input.fill("see you at 10");
  await host.getByRole("button", { name: "Save" }).click();

  const seen = guest.locator(".chat-line", { hasText: "see you at 10" });
  await expect(seen).toContainText("(edited)");
  await expect(guest.locator(".chat-line", { hasText: "see you at 9" })).toHaveCount(0);
  // Only the author gets the edit action; Up in an empty field edits the
  // last own line.
  await seen.hover();
  await expect(seen.getByRole("button", { name: "edit", exact: true })).toHaveCount(0);
  await input.press("ArrowUp");
  await expect(input).toHaveValue("see you at 10");
  await input.press("Escape");
  await expect(input).toHaveValue("");

  await hostCtx.close();
  await guestCtx.close();
});

test("star a room and find it under Starred", async ({ page }) => {
  await signUp(page, "fan");
  const slug = await createRoom(page, { name: "Starred one" });
  await page.goto(`/r/${slug}`);
  await page.getByRole("button", { name: "☆ Star" }).click();
  await expect(page.getByRole("button", { name: "★ Starred" })).toBeVisible();
  await page.goto("/?starred=1");
  await expect(page.locator(".room-card", { hasText: "Starred one" })).toBeVisible();
});
