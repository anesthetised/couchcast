import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ClockSync } from "~/lib/clock";
import type { RoomSocket } from "~/lib/ws";

function fakeSocket() {
  const sent: unknown[] = [];
  return { sent, socket: { send: (m: unknown) => (sent.push(m), true) } as unknown as RoomSocket };
}

describe("ClockSync", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(1_000_000);
  });
  afterEach(() => vi.useRealTimers());

  it("pings in a quick burst, then settles to every 5 s", () => {
    const { sent, socket } = fakeSocket();
    const c = new ClockSync(socket);
    c.start();
    expect(sent).toHaveLength(1);
    vi.advanceTimersByTime(5 * 300);
    expect(sent).toHaveLength(6);
    vi.advanceTimersByTime(4_999);
    expect(sent).toHaveLength(6);
    vi.advanceTimersByTime(1);
    expect(sent).toHaveLength(7);
    c.stop();
    vi.advanceTimersByTime(60_000);
    expect(sent).toHaveLength(7);
  });

  it("estimates the offset from the round-trip midpoint", () => {
    const c = new ClockSync(fakeSocket().socket);
    const updates = vi.fn();
    c.onUpdate = updates;
    // Sent at 1 000 000, answered 100 ms later; the server said 1 002 050.
    vi.setSystemTime(1_000_100);
    c.handlePong(1_000_000, 1_002_050);
    expect(c.rtt).toBe(100);
    expect(c.offset).toBe(2_000);
    expect(c.serverNow()).toBe(1_002_100);
    expect(updates).toHaveBeenCalledOnce();
  });

  it("takes the median so one slow round trip does not skew it", () => {
    const c = new ClockSync(fakeSocket().socket);
    vi.setSystemTime(1_000_100);
    c.handlePong(1_000_000, 1_000_550); // offset 500
    c.handlePong(1_000_000, 1_000_550); // offset 500
    c.handlePong(1_000_000, 1_009_050); // offset 9000, an outlier
    expect(c.offset).toBe(500);
  });
});
