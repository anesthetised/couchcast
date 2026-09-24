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

test("star a room and find it under Starred", async ({ page }) => {
  await signUp(page, "fan");
  const slug = await createRoom(page, { name: "Starred one" });
  await page.goto(`/r/${slug}`);
  await page.getByRole("button", { name: "☆ Star" }).click();
  await expect(page.getByRole("button", { name: "★ Starred" })).toBeVisible();
  await page.goto("/?starred=1");
  await expect(page.locator(".room-card", { hasText: "Starred one" })).toBeVisible();
});
