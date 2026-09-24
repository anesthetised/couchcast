import { expect, test } from "@playwright/test";

import { PASSWORD, signUp } from "../support/app";

const IPHONE =
  "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1";

test("the profile lists sessions and signs another device out", async ({ browser }) => {
  const laptopCtx = await browser.newContext();
  const phoneCtx = await browser.newContext({ userAgent: IPHONE });
  const laptop = await laptopCtx.newPage();
  const phone = await phoneCtx.newPage();

  const name = await signUp(laptop, "roamer");
  await phone.goto("/login");
  await phone.getByLabel("Username").fill(name);
  await phone.getByLabel("Password").fill(PASSWORD);
  await phone.getByRole("button", { name: "Log in", exact: true }).click();
  await expect(phone.locator(".usermenu .username")).toContainText(name);

  await laptop.goto("/me");
  const rows = laptop.locator("table.sessions tbody tr");
  await expect(rows).toHaveCount(2);
  await expect(rows.filter({ hasText: "this device" })).toHaveCount(1);
  const phoneRow = rows.filter({ hasText: "Safari 18 · iOS" });
  await expect(phoneRow).toHaveCount(1);

  await phoneRow.getByRole("button", { name: "Sign out" }).click();
  await expect(rows).toHaveCount(1);
  await expect(laptop.getByRole("button", { name: "Sign out everywhere else" })).toHaveCount(0);

  // The phone is anonymous on its next load.
  await phone.reload();
  await expect(phone.locator(".usermenu .username")).toHaveCount(0);

  await laptopCtx.close();
  await phoneCtx.close();
});
