import { afterEach, describe, expect, it, vi } from "vitest";

import { api, ApiError } from "~/lib/api";

function respond(status: number, body?: unknown, statusText = "") {
  const fetchMock = vi.fn().mockResolvedValue(
    new Response(body === undefined ? null : typeof body === "string" ? body : JSON.stringify(body), { status, statusText }),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends JSON with the session cookie and parses the answer", async () => {
    const fetchMock = respond(200, { ok: 1 });
    await expect(api("/api/v1/x", { method: "POST", body: "{}" })).resolves.toEqual({ ok: 1 });
    const [, init] = fetchMock.mock.calls[0]! as [string, RequestInit];
    expect(init.credentials).toBe("same-origin");
    expect(init.headers).toMatchObject({ Accept: "application/json", "Content-Type": "application/json" });
  });

  it("omits the content type without a body and returns nothing for 204", async () => {
    const fetchMock = respond(204);
    await expect(api("/api/v1/x")).resolves.toBeUndefined();
    const [, init] = fetchMock.mock.calls[0]! as [string, RequestInit];
    expect(init.headers).not.toHaveProperty("Content-Type");
  });

  it("raises the server's error message with the status", async () => {
    respond(409, { error: "name taken" });
    const err = await api("/api/v1/x").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err).toMatchObject({ status: 409, message: "name taken" });
  });

  it("falls back to the status text for a non-JSON error", async () => {
    respond(502, "<html>bad gateway</html>", "Bad Gateway");
    await expect(api("/api/v1/x")).rejects.toMatchObject({ status: 502, message: "Bad Gateway" });
  });
});
