import { expect, test } from "@playwright/test";

import { logIn, PASSWORD, signUp, uniq } from "../support/app";

test("register refuses a taken name and explains bad input", async ({ page }) => {
  const name = await signUp(page, "taken");
  await page.getByRole("button", { name: "Log out" }).click();
  await expect(page.getByRole("link", { name: "Log in" }).first()).toBeVisible();

  await page.goto("/register");
  await page.getByLabel("Username").fill(name.toUpperCase());
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Register" }).click();
  await expect(page.locator("form .error")).toHaveText("username is taken");

  // Editing clears the stale error; the browser refuses a bad name.
  const username = page.getByLabel("Username");
  await username.fill("bad name!");
  await expect(page.locator("form .error")).toHaveCount(0);
  await page.getByRole("button", { name: "Register" }).click();
  expect(await username.evaluate((el: HTMLInputElement) => el.validity.patternMismatch)).toBe(true);
  await expect(page).toHaveURL(/\/register$/);
});

test("log in, wrong password, log out, and come back to where you were", async ({ page }) => {
  const name = await signUp(page, "returning");
  await page.getByRole("button", { name: "Log out" }).click();

  // A page that needs an account sends you to log in and back.
  await page.goto("/me");
  await expect(page).toHaveURL(/\/login\?next=(%2F|\/)me$/);
  await page.getByLabel("Username").fill(name);
  await page.getByLabel("Password").fill("not-the-password");
  await page.getByRole("button", { name: "Log in", exact: true }).click();
  await expect(page.locator("form .error")).toHaveText("invalid username or password");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Log in", exact: true }).click();
  await expect(page).toHaveURL(/\/me$/);
  await expect(page.getByRole("heading", { name })).toBeVisible();

  // Logging out ends the session for this browser.
  await page.getByRole("button", { name: "Log out" }).click();
  await page.goto("/me");
  await expect(page).toHaveURL(/\/login/);
});

test("the profile changes the avatar colour and the password", async ({ browser }) => {
  const mainCtx = await browser.newContext();
  const otherCtx = await browser.newContext();
  const page = await mainCtx.newPage();
  const other = await otherCtx.newPage();
  const name = await signUp(page, "profiled");
  await logIn(other, name);

  await page.goto("/me");
  const teal = page.getByRole("radio", { name: "teal" });
  await teal.click();
  await expect(teal).toHaveAttribute("aria-checked", "true");
  await expect(page.locator(".usermenu .avatar")).toHaveClass(/c-teal/);
  await page.reload();
  await expect(page.getByRole("radio", { name: "teal" })).toHaveAttribute("aria-checked", "true");

  // Mismatched new passwords never reach the server.
  const next = `${PASSWORD}-new`;
  await page.getByLabel("Current password").fill(PASSWORD);
  await page.getByLabel("New password", { exact: true }).fill(next);
  await page.getByLabel("New password again").fill(`${next}x`);
  await page.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByText("The new passwords do not match.")).toBeVisible();

  await page.getByLabel("Current password").fill("wrong-password");
  await page.getByLabel("New password again").fill(next);
  await page.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByText("current password is wrong")).toBeVisible();

  await page.getByLabel("Current password").fill(PASSWORD);
  await page.getByRole("button", { name: "Change password" }).click();
  await expect(page.getByText("Password changed. Other devices were signed out.")).toBeVisible();

  // This browser stays in; the other one is out and needs the new password.
  await page.reload();
  await expect(page.locator(".usermenu .username")).toContainText(name);
  await other.reload();
  await expect(other.locator(".usermenu .username")).toHaveCount(0);
  await logIn(other, name, next);

  await mainCtx.close();
  await otherCtx.close();
});

test("anonymous visitors see the public pages and are asked to log in to act", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("link", { name: "Log in" }).first()).toBeVisible();
  await page.goto("/new");
  await expect(page).toHaveURL(/\/login\?next=(%2F|\/)new$/);
  await page.goto(`/r/${uniq("missing").toLowerCase().replace(/_/g, "-")}`);
  await expect(page.getByText(/not found|does not exist/i).first()).toBeVisible();
});
