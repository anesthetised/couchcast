import { expect, type Page } from "@playwright/test";
import pg from "pg";

// Test users get unique names per run; the password never matters.
export const PASSWORD = "e2e-password-123";

export function uniq(prefix: string): string {
  return `${prefix}_${Math.random().toString(36).slice(2, 10)}`;
}

// signUp registers a fresh user through the form and waits for the app
// to show them signed in.
export async function signUp(page: Page, prefix = "user"): Promise<string> {
  const name = uniq(prefix);
  await page.goto("/register");
  await page.getByLabel("Username").fill(name);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Register" }).click();
  await expect(page.locator(".usermenu .username")).toContainText(name);
  return name;
}

// createRoom goes through the API with the page's session: most tests
// are about what happens inside a room, not about the form.
export async function createRoom(
  page: Page,
  body: { name?: string; visibility?: "public" | "private"; description?: string; firstUrl?: string } = {},
): Promise<string> {
  const slug = uniq("room").replace(/_/g, "-").toLowerCase();
  const res = await page.request.post("/api/v1/rooms", {
    data: { name: body.name ?? "E2E room", slug, visibility: body.visibility ?? "public", description: body.description, firstUrl: body.firstUrl },
  });
  expect(res.status(), await res.text()).toBe(201);
  return slug;
}

// sql runs a statement against the e2e database, for what no UI does
// (granting the admin role).
export async function sql(text: string, values: unknown[] = []): Promise<pg.QueryResult> {
  const client = new pg.Client({ connectionString: process.env.E2E_DATABASE_URL });
  await client.connect();
  try {
    return await client.query(text, values);
  } finally {
    await client.end();
  }
}

// readyVideo stores a YouTube video as already ingested, so a room can
// queue and "play" it without the ingest worker (the files do not exist;
// tests that need this are about room behaviour, not pixels).
export async function readyVideo(title = "E2E video", durationMs = 600_000): Promise<string> {
  const id = Array.from({ length: 11 }, () => "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"[Math.floor(Math.random() * 64)]).join("");
  const url = `https://www.youtube.com/watch?v=${id}`;
  await sql(
    `INSERT INTO media (source_key, source_url, title, duration_ms, status, progress, s3_prefix)
     VALUES ($1, $2, $3, $4, 'ready', 1, $5)`,
    [`youtube:${id}`, url, title, durationMs, `media/e2e-${id}/`],
  );
  return url;
}

export async function makeAdmin(username: string) {
  await sql("UPDATE users SET role = 'admin' WHERE username = $1", [username]);
}

// chat sends a line and waits for it to come back from the server.
export async function chat(page: Page, text: string) {
  await page.getByPlaceholder("Say something").fill(text);
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.locator(".chat-line", { hasText: text }).last()).toBeVisible();
}
