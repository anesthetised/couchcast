import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { RoomSocket } from "~/lib/ws";
import type { ServerMessage } from "~/protocol";
import { FakeWebSocket } from "~/test/fakeWebSocket";

const last = () => FakeWebSocket.all.at(-1)!;

describe("RoomSocket", () => {
  beforeEach(() => {
    FakeWebSocket.all = [];
    vi.stubGlobal("WebSocket", FakeWebSocket);
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("connects to the room's endpoint and reports status", () => {
    const s = new RoomSocket("movie-night");
    const statuses: string[] = [];
    s.onStatus = (st) => statuses.push(st);
    s.connect();
    expect(last().url).toBe(`ws://${location.host}/api/v1/rooms/movie-night/ws`);
    expect(s.send({ type: "pause" })).toBe(false);
    last().accept();
    expect(s.send({ type: "pause" })).toBe(true);
    expect(JSON.parse(last().sent[0]!)).toEqual({ type: "pause" });
    expect(statuses).toEqual(["connecting", "open"]);
    expect(s.opened).toBe(true);
  });

  it("passes messages to subscribers and skips malformed frames", () => {
    const s = new RoomSocket("r");
    const got: ServerMessage[] = [];
    const off = s.subscribe((m) => got.push(m));
    s.connect();
    last().accept();
    last().deliver({ type: "typing", username: "bob" });
    last().deliver("{broken");
    off();
    last().deliver({ type: "typing", username: "eve" });
    expect(got).toEqual([{ type: "typing", username: "bob" }]);
  });

  it("reconnects with exponential backoff and resets after a successful open", async () => {
    const s = new RoomSocket("r");
    const drops: [string, number][] = [];
    s.onDrop = (reason, attempts) => (drops.push([reason, attempts]), true);
    s.connect();
    last().accept();
    last().drop(1006, "");
    await vi.advanceTimersByTimeAsync(499);
    expect(FakeWebSocket.all).toHaveLength(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeWebSocket.all).toHaveLength(2);
    last().drop(1006, "");
    await vi.advanceTimersByTimeAsync(999);
    expect(FakeWebSocket.all).toHaveLength(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeWebSocket.all).toHaveLength(3);
    expect(drops).toEqual([
      ["", 1],
      ["", 2],
    ]);
    last().accept();
    expect(s.attempts).toBe(0);
  });

  it("stops reconnecting when onDrop says so", async () => {
    const s = new RoomSocket("r");
    s.onDrop = () => false;
    s.connect();
    last().drop(1006, "");
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeWebSocket.all).toHaveLength(1);
  });

  it("treats a policy close with a reason as a kick", async () => {
    const s = new RoomSocket("r");
    const got: ServerMessage[] = [];
    s.subscribe((m) => got.push(m));
    s.connect();
    last().accept();
    last().drop(1008, "you were banned");
    expect(s.status).toBe("kicked");
    expect(got).toEqual([{ type: "kicked", reason: "you were banned" }]);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeWebSocket.all).toHaveLength(1);
  });

  it("stays down after a kicked message and after close()", async () => {
    const s = new RoomSocket("r");
    s.connect();
    last().accept();
    last().deliver({ type: "kicked", reason: "room deleted" });
    last().drop(1000, "");
    expect(s.status).toBe("kicked");

    const t = new RoomSocket("r2");
    t.connect();
    t.close();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeWebSocket.all.filter((w) => w.url.includes("/r2/"))).toHaveLength(1);
    expect(t.status).toBe("closed");
  });
});
