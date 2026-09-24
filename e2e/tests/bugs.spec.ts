import { expect, test } from "@playwright/test";

import { createRoom, makeAdmin, signUp } from "../support/app";

test("report a problem and resolve it in the admin panel", async ({ page }) => {
  const name = await signUp(page, "reporter");
  const slug = await createRoom(page);
  await page.goto(`/r/${slug}`);

  await page.getByRole("button", { name: "Report a problem" }).click();
  const dialog = page.getByRole("dialog", { name: "Report a problem" });
  await dialog.getByRole("radio", { name: "Sync" }).click();
  await dialog.getByLabel("What happened?").fill("e2e: the video drifts");
  await dialog.locator("summary", { hasText: "What will be sent" }).click();
  await expect(dialog.locator(".bug-preview pre")).toContainText(`"slug": "${slug}"`);
  await dialog.getByRole("button", { name: "Send" }).click();
  await expect(dialog).toContainText("Thanks");
  await dialog.getByRole("button", { name: "Close" }).click();

  await makeAdmin(name);
  await page.goto("/admin");
  await page.getByRole("button", { name: "Bugs" }).click();
  const row = page.locator(".bug-item", { hasText: "e2e: the video drifts" });
  await expect(row).toBeVisible();
  await row.locator(".bug-row").click();
  await expect(row.locator(".bug-facts")).toContainText(`/r/${slug}`);
  await row.getByPlaceholder("Note (optional)").fill("checked by e2e");
  await row.getByRole("button", { name: "Resolve" }).click();
  await expect(row).toHaveCount(0);

  await page.getByRole("button", { name: "Resolved", exact: true }).click();
  await expect(page.locator(".bug-item", { hasText: "e2e: the video drifts" })).toBeVisible();
});
