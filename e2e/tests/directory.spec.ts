import { expect, test } from "@playwright/test";

import { signUp, uniq } from "../support/app";

test("the directory searches and filters, keeping both in the URL", async ({ page }) => {
  await signUp(page, "browser");
  const tag = uniq("dir").replace(/_/g, "");
  for (const name of [`${tag} alpha`, `${tag} beta`]) {
    const res = await page.request.post("/api/v1/rooms", { data: { name, visibility: "public" } });
    expect(res.status()).toBe(201);
  }
  const cards = page.locator(".directory .room-card");

  await page.goto("/");
  await page.getByLabel("Search rooms").fill(`${tag} alp`);
  await expect(page).toHaveURL(new RegExp(`[?&]q=${tag}`));
  await expect(cards).toHaveCount(1);
  await expect(cards.first()).toContainText(`${tag} alpha`);

  // A reload or a shared link shows the same results.
  await page.reload();
  await expect(page.getByLabel("Search rooms")).toHaveValue(`${tag} alp`);
  await expect(cards).toHaveCount(1);

  await page.getByLabel("Search rooms").fill(tag);
  await expect(cards).toHaveCount(2);
  await page.getByRole("button", { name: "Mine" }).click();
  await expect(page).toHaveURL(/[?&]mine=1/);
  await expect(page.getByRole("button", { name: "Mine" })).toHaveAttribute("aria-pressed", "true");
  await expect(cards).toHaveCount(2);
  await page.getByLabel("Sort rooms").selectOption("newest");
  await expect(page).toHaveURL(/[?&]sort=newest/);
  await expect(cards.first()).toContainText(`${tag} beta`, { timeout: 7_000 });

  // Filters that match nothing say so.
  await page.getByLabel("Search rooms").fill(`${tag} nothing`);
  await expect(cards).toHaveCount(0);
});

test("an announced session shows as upcoming with a countdown", async ({ page }) => {
  await signUp(page, "planner");
  const name = `${uniq("sched").replace(/_/g, "")} premiere`;
  const at = new Date(Date.now() + 30 * 60_000 + 20_000).toISOString();
  const res = await page.request.post("/api/v1/rooms", { data: { name, visibility: "public", scheduledAt: at } });
  expect(res.status(), await res.text()).toBe(201);

  await page.goto("/?upcoming=1");
  await expect(page.getByRole("button", { name: "Upcoming" })).toHaveAttribute("aria-pressed", "true");
  const card = page.locator(".directory .room-card", { hasText: name });
  await expect(card).toBeVisible();
  await expect(card.locator(".upcoming")).toHaveText(/Starts in (30|31) min/);
});
