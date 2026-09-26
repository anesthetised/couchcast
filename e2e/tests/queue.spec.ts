import { expect, test, type Page } from "@playwright/test";

import { createRoom, readyVideo, signUp } from "../support/app";

async function add(page: Page, url: string) {
  await page.getByPlaceholder("Paste a YouTube or video link").fill(url);
  await page.getByRole("button", { name: "Add", exact: true }).click();
}

const titles = (page: Page) => page.locator(".up-next .queue .list .queue-item .queue-title");

test("fair queue lets everyone take turns", async ({ browser }) => {
  const hostCtx = await browser.newContext();
  const guestCtx = await browser.newContext();
  const host = await hostCtx.newPage();
  const guest = await guestCtx.newPage();

  await signUp(host, "host");
  const [first, h2, h3, g1] = await Promise.all([readyVideo("Host one"), readyVideo("Host two"), readyVideo("Host three"), readyVideo("Guest one")]);
  const slug = await createRoom(host, { firstUrl: first });
  await host.goto(`/r/${slug}`);
  await add(host, h2);
  await add(host, h3);
  await expect(titles(host)).toHaveText(["Host one", "Host two", "Host three"]);

  await signUp(guest, "guest");
  await guest.goto(`/r/${slug}`);
  await add(guest, g1);
  await expect(titles(host)).toHaveText(["Host one", "Host two", "Host three", "Guest one"]);

  // Turn it on from the room options: the guest's video comes next.
  await host.locator("summary", { hasText: "Options" }).click();
  await host.getByLabel("Fair queue (take turns)").check();
  await expect(titles(host)).toHaveText(["Host one", "Guest one", "Host two", "Host three"]);
  await expect(titles(guest)).toHaveText(["Host one", "Guest one", "Host two", "Host three"]);
  await expect(host.locator(".up-next .section-title")).toContainText("taking turns");

  await hostCtx.close();
  await guestCtx.close();
});

test("the host reorders, removes, skips and replays", async ({ page }) => {
  await signUp(page, "curator");
  const [a, b, c] = [await readyVideo("Alpha"), await readyVideo("Bravo"), await readyVideo("Charlie")];
  const slug = await createRoom(page, { firstUrl: a });
  await page.goto(`/r/${slug}`);
  await add(page, b);
  await add(page, c);
  const all = page.locator(".up-next .queue .list .queue-item:not(.played-item)");
  await expect(all).toHaveCount(3);

  // Charlie moves above Bravo.
  await all.filter({ hasText: "Charlie" }).getByRole("button", { name: "Move up" }).click();
  await expect(all.nth(1)).toContainText("Charlie");

  // Bravo goes.
  await all.filter({ hasText: "Bravo" }).getByRole("button", { name: "Remove" }).click();
  await expect(all).toHaveCount(2);

  // Next: Alpha is played, Charlie is current.
  await page.locator("body").press("n");
  await expect(page.locator(".queue-item.current")).toContainText("Charlie");
  await expect(page.locator(".chat-line.system", { hasText: "skipped" })).toBeVisible();
  const played = page.locator("details.played");
  await played.locator("summary").click();
  await expect(played.locator(".queue-item")).toContainText("Alpha");

  // Played again: Alpha is back at the end of the queue.
  await played.getByRole("button", { name: "play again" }).click();
  await expect(all.last()).toContainText("Alpha");
  await expect(page.locator(".chat-line.system", { hasText: "re-added" })).toBeVisible();

  // Clearing asks first and keeps the current video.
  await add(page, await readyVideo("Delta"));
  await expect(all).toHaveCount(3);
  page.once("dialog", (d) => void d.accept());
  await page.locator(".queue .section-title").getByRole("button", { name: "Clear" }).click();
  await expect(all).toHaveCount(1);
  await expect(all.first()).toContainText("Charlie");
});

test("in vote mode viewers reorder by votes and vote to skip", async ({ browser }) => {
  const hostCtx = await browser.newContext();
  const guestCtx = await browser.newContext();
  const host = await hostCtx.newPage();
  const guest = await guestCtx.newPage();
  await signUp(host, "voterhost");
  const slug = await createRoom(host, { firstUrl: await readyVideo("Now") });
  await host.goto(`/r/${slug}`);
  await add(host, await readyVideo("Maybe"));
  await add(host, await readyVideo("Popular"));
  await host.locator("summary", { hasText: "Options" }).click();
  await host.getByLabel("Vote mode").check();

  await signUp(guest, "voter");
  await guest.goto(`/r/${slug}`);
  const items = guest.locator(".up-next .queue .list .queue-item");
  await expect(items).toHaveCount(3);
  await items.filter({ hasText: "Popular" }).getByRole("button", { name: /Vote up/ }).click();
  await expect(items.nth(1)).toContainText("Popular");
  await expect(items.nth(1).getByRole("button", { name: "Vote up (1)" })).toHaveAttribute("aria-pressed", "true");

  // Two viewers, half must agree: the guest's vote is enough.
  await guest.getByRole("button", { name: /Vote to skip/ }).click();
  await expect(guest.locator(".queue-item.current")).toContainText("Popular");
  await expect(host.locator(".chat-line.system", { hasText: "by vote" })).toBeVisible();

  await hostCtx.close();
  await guestCtx.close();
});
