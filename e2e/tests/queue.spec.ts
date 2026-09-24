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
