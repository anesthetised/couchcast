import { expect, test, type Page } from "@playwright/test";

import { makeAdmin, readyVideo, signUp, sql } from "../support/app";

async function mediaID(url: string): Promise<string> {
  const id = url.split("v=")[1]!;
  const res = await sql("SELECT id FROM media WHERE source_key = $1", [`youtube:${id}`]);
  return res.rows[0].id as string;
}

async function report(page: Page, url: string) {
  const res = await page.request.post(`/api/v1/media/${await mediaID(url)}/reports`, { data: { reason: "nsfw", comment: "e2e" } });
  expect(res.status(), await res.text()).toBe(204);
}

const tab = (page: Page, name: string) => page.locator("nav.tabs").getByRole("button", { name, exact: true });

test("admins handle reports: dismiss one, delete and block another", async ({ browser }) => {
  const adminCtx = await browser.newContext();
  const userCtx = await browser.newContext();
  const admin = await adminCtx.newPage();
  const user = await userCtx.newPage();
  const adminName = await signUp(admin, "root");
  await makeAdmin(adminName);
  await signUp(user, "snitch");

  const harmless = await readyVideo("Harmless clip");
  const bad = await readyVideo("Bad clip");
  await report(user, harmless);
  await report(user, bad);

  await admin.goto("/admin");
  await tab(admin, "Reports").click();
  await expect(admin).toHaveURL(/\?tab=reports$/);
  const card = (title: string) => admin.locator(".card, li, .row, div").filter({ hasText: title }).filter({ has: admin.getByRole("button", { name: "Dismiss" }) }).last();
  await expect(card("Harmless clip")).toBeVisible();
  await card("Harmless clip").getByRole("button", { name: "Dismiss" }).click();
  await expect(admin.getByText("Harmless clip")).toHaveCount(0);

  // Cancelling the reason prompt changes nothing; accepting deletes and blocks.
  admin.once("dialog", (d) => void d.dismiss());
  await card("Bad clip").getByRole("button", { name: "Delete & block" }).click();
  await expect(admin.getByText("Bad clip")).toBeVisible();
  admin.once("dialog", (d) => void d.accept("dmca"));
  await card("Bad clip").getByRole("button", { name: "Delete & block" }).click();
  await expect(admin.getByText("Bad clip")).toHaveCount(0);
  expect((await sql("SELECT count(*)::int AS n FROM media WHERE title = 'Bad clip'")).rows[0].n).toBe(0);

  // The tab survives a reload because it lives in the URL.
  await tab(admin, "Blocklist").click();
  await admin.reload();
  await expect(tab(admin, "Blocklist")).toHaveAttribute("aria-pressed", "true");
  await expect(admin.getByText(`youtube:${bad.split("v=")[1]}`)).toBeVisible();

  await adminCtx.close();
  await userCtx.close();
});

test("admins ban and unban users, and the audit log records it", async ({ browser }) => {
  const adminCtx = await browser.newContext();
  const userCtx = await browser.newContext();
  const admin = await adminCtx.newPage();
  const user = await userCtx.newPage();
  const adminName = await signUp(admin, "warden");
  await makeAdmin(adminName);
  const name = await signUp(user, "troublemaker");

  await admin.goto("/admin?tab=users");
  await admin.getByLabel("Search users").fill(name);
  const row = admin.locator("li.row", { hasText: name });
  await expect(row).toBeVisible();

  admin.once("dialog", (d) => void d.dismiss());
  await row.getByRole("button", { name: "Ban" }).click();
  await expect(row.getByRole("button", { name: "Ban" })).toBeVisible();

  admin.once("dialog", (d) => void d.accept("spam links"));
  await row.getByRole("button", { name: "Ban" }).click();
  await expect(row).toContainText("banned: spam links");
  // Their sessions end at once.
  await user.reload();
  await expect(user.locator(".usermenu .username")).toHaveCount(0);

  await row.getByRole("button", { name: "Unban" }).click();
  await expect(row.getByRole("button", { name: "Ban" })).toBeVisible();

  await tab(admin, "Audit").click();
  await expect(admin.getByText("user.ban").first()).toBeVisible();
  await expect(admin.getByText("user.unban").first()).toBeVisible();

  await adminCtx.close();
  await userCtx.close();
});

test("non-admins get no admin panel", async ({ page }) => {
  await signUp(page, "curious");
  await page.goto("/admin");
  await expect(page.getByText("Administrator role required.")).toBeVisible();
  expect((await page.request.get("/api/v1/admin/stats")).status()).toBe(403);
});
