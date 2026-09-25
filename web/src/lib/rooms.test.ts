import { afterEach, describe, expect, it, vi } from "vitest";

import { rooms } from "~/lib/rooms";

describe("rooms.directory", () => {
  afterEach(() => vi.unstubAllGlobals());

  function capture() {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ rooms: [], total: 0 }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    return () => fetchMock.mock.calls.at(-1)![0] as string;
  }

  it("leaves defaults out of the query string", async () => {
    const url = capture();
    await rooms.directory({ sort: "active", page: 1 });
    expect(url()).toBe("/api/v1/rooms");
  });

  it("encodes every filter", async () => {
    const url = capture();
    await rooms.directory({ q: "friday night", sort: "new", live: true, private: true, mine: true, starred: true, upcoming: true, page: 3, perPage: 12 });
    const qs = new URL(url(), "http://x").searchParams;
    expect(Object.fromEntries(qs)).toEqual({
      q: "friday night",
      sort: "new",
      live: "1",
      private: "1",
      mine: "1",
      starred: "1",
      upcoming: "1",
      page: "3",
      perPage: "12",
    });
  });
});
