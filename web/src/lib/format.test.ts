import { describe, expect, it } from "vitest";

import { formatAgo, formatDuration, formatEta, formatSpeed, formatStart, formatTime, fromLocalInput, progressDetail, toLocalInput } from "~/lib/format";

describe("formatTime", () => {
  it.each([
    [0, "0:00"],
    [-5, "0:00"],
    [65_400, "1:05"],
    [3_600_000, "1:00:00"],
    [3_723_000, "1:02:03"],
  ])("%d ms → %s", (ms, want) => {
    expect(formatTime(ms)).toBe(want);
  });
});

describe("formatAgo", () => {
  const now = 1_800_000_000_000;
  it.each([
    [now - 10_000, "just now"],
    [now + 5_000, "just now"],
    [now - 5 * 60_000, "5m ago"],
    [now - 3 * 3_600_000, "3h ago"],
    [now - 47 * 3_600_000, "47h ago"],
    [now - 3 * 86_400_000, "3d ago"],
  ])("%d → %s", (ms, want) => {
    expect(formatAgo(ms, now)).toBe(want);
  });
});

describe("durations and speeds", () => {
  it("formats coarse durations", () => {
    expect(formatDuration(10_000)).toBe("< 1m");
    expect(formatDuration(45 * 60_000)).toBe("45m");
    expect(formatDuration(192 * 60_000)).toBe("3h 12m");
  });

  it("formats speeds", () => {
    expect(formatSpeed(500)).toBe("500 B/s");
    expect(formatSpeed(340 * 1024)).toBe("340 KB/s");
    expect(formatSpeed(1.2 * 1024 * 1024)).toBe("1.2 MB/s");
  });

  it("formats time left", () => {
    expect(formatEta(0)).toBe("1 s");
    expect(formatEta(12_000)).toBe("12 s");
    expect(formatEta(3 * 60_000)).toBe("3 min");
    expect(formatEta(65 * 60_000)).toBe("1h 05m");
  });
});

describe("progressDetail", () => {
  it("describes steps in flight only", () => {
    expect(progressDetail({ status: "ready", progress: 1 })).toBeNull();
    expect(progressDetail({ status: "downloading", progress: 0.426 })).toBe("43%");
    expect(progressDetail({ status: "packaging", progress: 0.5, speedBps: 2048, etaMs: 12_000 })).toBe("50% · 2 KB/s · 12 s left");
  });
});

describe("local datetime inputs", () => {
  it("round-trips through the input format", () => {
    const iso = new Date(2026, 8, 25, 20, 5).toISOString();
    const local = toLocalInput(iso);
    expect(local).toBe("2026-09-25T20:05");
    expect(fromLocalInput(local)).toBe(iso);
  });

  it("treats empty and invalid values as none", () => {
    expect(toLocalInput(null)).toBe("");
    expect(fromLocalInput("")).toBeNull();
    expect(fromLocalInput("nonsense")).toBeNull();
  });
});

describe("formatStart", () => {
  const now = new Date(2026, 8, 25, 12, 0).getTime();
  it("says how far away a start is", () => {
    expect(formatStart(now - 5 * 60_000, now)).toBe("Started 5m ago");
    expect(formatStart(now + 30_000, now)).toBe("Starts in a moment");
    expect(formatStart(now + 12 * 60_000, now)).toBe("Starts in 12 min");
    expect(formatStart(new Date(2026, 8, 25, 20, 0).getTime(), now)).toMatch(/^Starts today /);
    expect(formatStart(new Date(2026, 8, 27, 20, 0).getTime(), now)).toMatch(/^Starts \S+ /);
    expect(formatStart(new Date(2026, 10, 1, 20, 0).getTime(), now)).toMatch(/^Starts /);
  });
});
