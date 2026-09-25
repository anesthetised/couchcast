import { beforeEach, describe, expect, it, vi } from "vitest";

// The module keeps its buffers in module state: each test gets a fresh one.
async function load() {
  vi.resetModules();
  return import("~/lib/diagnostics");
}

describe("diagnostics", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("keeps the last 100 events and truncates long text", async () => {
    const d = await load();
    for (let i = 0; i < 120; i++) d.logEvent("app", `event ${i}`);
    d.logEvent("server", "x".repeat(900));
    const { events } = d.collect();
    expect(events).toHaveLength(100);
    expect(events[0]!.text).toBe("event 21");
    expect(events.at(-1)!.text).toHaveLength(500);
  });

  it("records sync corrections but collapses runs of idle ticks", async () => {
    const d = await load();
    d.logSync({ driftMs: 10.4, rate: 1, action: "idle" });
    d.logSync({ driftMs: 12, rate: 1, action: "idle" });
    d.logSync({ driftMs: 300.6, rate: 0.95, action: "nudge" });
    d.logSync({ driftMs: 5, rate: 1, action: "idle" });
    const { sync } = d.collect();
    expect(sync.map((s) => s.action)).toEqual(["idle", "nudge", "idle"]);
    expect(sync[1]!.driftMs).toBe(301);
  });

  it("includes probes, reports a throwing one and forgets unregistered ones", async () => {
    const d = await load();
    const off = d.registerProbe("session", () => ({ room: "test" }));
    d.registerProbe("media", () => {
      throw new Error("no player");
    });
    let snap = d.collect();
    expect(snap.session).toEqual({ room: "test" });
    expect(snap.media).toEqual({ error: "no player" });
    expect(snap.app.route).toBe(location.pathname + location.search);
    expect(snap.device.viewport.width).toBe(window.innerWidth);
    off();
    snap = d.collect();
    expect(snap.session).toBeUndefined();
  });

  it("records uncaught errors once the global handlers are installed", async () => {
    const d = await load();
    d.installGlobalHandlers();
    d.installGlobalHandlers(); // idempotent
    window.dispatchEvent(new ErrorEvent("error", { message: "boom", filename: "app.js", lineno: 3 }));
    const events = d.collect().events.filter((e) => e.kind === "window");
    expect(events).toHaveLength(1);
    expect(events[0]!.text).toBe("boom");
  });

  it("has no frame without a decoded video", async () => {
    const d = await load();
    expect(await d.captureFrame()).toBeNull();
    d.registerVideo(() => ({ readyState: 1, videoWidth: 0 }) as HTMLVideoElement);
    expect(await d.captureFrame()).toBeNull();
  });

  it("renders buffered ranges in rounded seconds", async () => {
    const d = await load();
    const ranges = { length: 2, start: (i: number) => [0, 10.04][i]!, end: (i: number) => [4.26, 12][i]! } as TimeRanges;
    expect(d.bufferedRanges(ranges)).toEqual([
      [0, 4.3],
      [10, 12],
    ]);
  });
});
