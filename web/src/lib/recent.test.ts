import { beforeEach, describe, expect, it, vi } from "vitest";

async function load() {
  vi.resetModules();
  return import("~/lib/recent");
}

describe("recent rooms", () => {
  beforeEach(() => localStorage.clear());

  it("keeps the last five visits, newest first, one per room", async () => {
    const r = await load();
    for (const slug of ["a", "b", "c", "d", "e", "f"]) r.recordVisit(slug, slug.toUpperCase());
    r.recordVisit("c", "C again");
    expect(r.recent().map((x) => x.slug)).toEqual(["c", "f", "e", "d", "b"]);
    expect(r.recent()[0]!.name).toBe("C again");
  });

  it("persists across reloads and forgets rooms on request", async () => {
    let r = await load();
    r.recordVisit("a", "A");
    r.recordVisit("b", "B");
    r = await load();
    expect(r.recent().map((x) => x.slug)).toEqual(["b", "a"]);
    r.forgetVisit("b");
    r = await load();
    expect(r.recent().map((x) => x.slug)).toEqual(["a"]);
  });

  it("ignores junk in storage", async () => {
    localStorage.setItem("couchcast.recent", JSON.stringify([{ slug: 1 }, { slug: "ok", name: "OK", at: 1 }, "x"]));
    expect((await load()).recent().map((x) => x.slug)).toEqual(["ok"]);
    localStorage.setItem("couchcast.recent", "{");
    expect((await load()).recent()).toEqual([]);
  });
});
