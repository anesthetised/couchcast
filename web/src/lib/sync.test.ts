import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ClockSync } from "~/lib/clock";
import { Synchronizer, type SyncDebug } from "~/lib/sync";
import type { Playback } from "~/protocol";

// A <video> stand-in with the fields the synchronizer reads and writes.
function fakeVideo() {
  return {
    currentTime: 0,
    paused: true,
    playbackRate: 1,
    readyState: 4,
    play: vi.fn(function (this: { paused: boolean }) {
      this.paused = false;
      return Promise.resolve();
    }),
    pause: vi.fn(function (this: { paused: boolean }) {
      this.paused = true;
    }),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  };
}

const clockAt = (now: number) => ({ serverNow: () => now }) as unknown as ClockSync;

const playing = (positionMs: number, atServerMs: number, rate = 1): Playback => ({
  itemId: "item",
  playing: true,
  positionMs,
  atServerMs,
  rate,
  seq: 1,
});

function setup(now = 10_000) {
  const video = fakeVideo();
  const s = new Synchronizer(video as unknown as HTMLVideoElement, clockAt(now));
  const log: SyncDebug[] = [];
  s.onDebug = (d) => log.push(d);
  return { video, s, log };
}

describe("Synchronizer", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("computes the target from the server clock and the rate", () => {
    const { s } = setup(10_000);
    s.update(playing(5_000, 8_000, 1.5));
    expect(s.targetMs()).toBe(8_000);
    s.update({ ...playing(5_000, 8_000), playing: false });
    expect(s.targetMs()).toBe(5_000);
    s.update({ ...playing(0, 0), itemId: null });
    expect(s.targetMs()).toBeNull();
  });

  it("leaves a video within the dead band alone", () => {
    const { video, s, log } = setup();
    video.paused = false;
    video.currentTime = 5.02;
    s.update(playing(5_000, 10_000));
    expect(video.playbackRate).toBe(1);
    expect(log.at(-1)!.action).toBe("idle");
  });

  it("nudges the rate towards the target, relative to the room speed", () => {
    const { video, s, log } = setup();
    video.paused = false;
    video.currentTime = 5.3; // 300 ms ahead
    s.update(playing(5_000, 10_000, 2));
    expect(video.playbackRate).toBeCloseTo(1.9);
    expect(log.at(-1)!.action).toBe("nudge");
    video.currentTime = 4.7; // 300 ms behind
    s.update(playing(5_000, 10_000, 2));
    expect(video.playbackRate).toBeCloseTo(2.1);
  });

  it("seeks slightly ahead when far off", () => {
    const { video, s, log } = setup();
    video.paused = false;
    video.currentTime = 1;
    s.update(playing(5_000, 10_000));
    expect(video.currentTime).toBeCloseTo(5.15);
    expect(log.at(-1)!.action).toBe("seek");
  });

  it("starts a paused video when the room plays, and pauses it when the room pauses", () => {
    const { video, s } = setup();
    video.currentTime = 5;
    s.update(playing(5_000, 10_000));
    expect(video.play).toHaveBeenCalled();
    s.update({ ...playing(5_000, 10_000), playing: false });
    expect(video.pause).toHaveBeenCalled();
    expect(video.paused).toBe(true);
  });

  it("reports blocked autoplay and clears it once playback starts", async () => {
    const { video, s } = setup();
    const blocked = vi.fn();
    s.onBlocked = blocked;
    video.play.mockImplementationOnce(() => Promise.reject(new DOMException("no gesture", "NotAllowedError")));
    video.currentTime = 5;
    s.update(playing(5_000, 10_000));
    await vi.waitFor(() => expect(blocked).toHaveBeenCalledWith(true));
    s.resume();
    await vi.waitFor(() => expect(blocked).toHaveBeenCalledWith(false));
  });

  it("does nothing while suspended or before the media has loaded", () => {
    const { video, s, log } = setup();
    s.suspended = true;
    s.update(playing(5_000, 10_000));
    s.suspended = false;
    video.readyState = 0;
    s.update(playing(5_000, 10_000));
    expect(log).toHaveLength(0);
    expect(video.play).not.toHaveBeenCalled();
  });

  it("steps on its own interval until stopped", () => {
    const { video, s, log } = setup();
    video.paused = false;
    video.currentTime = 5;
    s.update(playing(5_000, 10_000));
    s.start();
    const before = log.length;
    vi.advanceTimersByTime(1_000);
    expect(log.length).toBeGreaterThan(before);
    s.stop();
    const after = log.length;
    vi.advanceTimersByTime(1_000);
    expect(log.length).toBe(after);
  });
});
