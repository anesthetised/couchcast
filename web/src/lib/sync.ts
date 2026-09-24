import type { ClockSync } from "~/lib/clock";
import { logSync } from "~/lib/diagnostics";
import type { Playback } from "~/protocol";

export interface SyncDebug {
  targetMs: number;
  driftMs: number;
  rate: number;
  action: "idle" | "nudge" | "seek" | "pause" | "play";
}

// Thresholds in milliseconds.
const DEADBAND = 50; // below this, leave the video alone
const SEEK_AT = 1000; // above this, seek instead of nudging
const NUDGE = 0.05; // playbackRate adjustment while converging

// Synchronizer keeps a <video> on the server clock. It never talks to the
// server: playback state arrives from the room store, and the local video
// is nudged (playbackRate) or seeked to match.
export class Synchronizer {
  private timer: number | null = null;
  private playback: Playback | null = null;
  private seeking = false;
  onDebug: (d: SyncDebug) => void = () => {};
  private debug(d: SyncDebug) {
    logSync(d);
    this.onDebug(d);
  }
  // Called when the browser refuses to start playback without a gesture
  // (and again with false once it plays); the UI shows a tap-to-play gate.
  onBlocked: (blocked: boolean) => void = () => {};
  // Set while the media element is loading a new source.
  suspended = false;
  private blocked = false;

  constructor(
    private readonly video: HTMLVideoElement,
    private readonly clock: ClockSync,
  ) {}

  start() {
    this.stop();
    this.timer = window.setInterval(() => this.step(), 250);
    this.video.addEventListener("seeked", this.onSeeked);
  }

  stop() {
    if (this.timer !== null) window.clearInterval(this.timer);
    this.timer = null;
    this.video.removeEventListener("seeked", this.onSeeked);
  }

  private onSeeked = () => {
    this.seeking = false;
  };

  update(p: Playback) {
    this.playback = p;
    this.step();
  }

  // targetMs is where the server says we should be right now.
  targetMs(): number | null {
    const p = this.playback;
    if (!p || p.itemId === null) return null;
    if (!p.playing) return p.positionMs;
    return p.positionMs + (this.clock.serverNow() - p.atServerMs) * p.rate;
  }

  private step() {
    const p = this.playback;
    const v = this.video;
    if (!p || p.itemId === null || this.suspended || v.readyState === 0) return;

    const target = this.targetMs();
    if (target === null) return;
    const drift = v.currentTime * 1000 - target;

    const base = p.rate > 0 ? p.rate : 1;
    if (!p.playing) {
      if (!v.paused) v.pause();
      v.playbackRate = base;
      if (Math.abs(drift) > 200 && !this.seeking) {
        this.seekTo(target);
        this.debug({ targetMs: target, driftMs: drift, rate: 1, action: "seek" });
        return;
      }
      this.debug({ targetMs: target, driftMs: drift, rate: 1, action: "pause" });
      return;
    }

    if (v.paused) {
      if (!this.blocked) this.tryPlay();
      this.debug({ targetMs: target, driftMs: drift, rate: v.playbackRate, action: "play" });
    }

    if (this.seeking) return;

    // Nudges are relative to the room's speed, so 1.5× stays 1.5×.
    if (Math.abs(drift) > SEEK_AT) {
      // Land slightly ahead: the seek itself takes time.
      this.seekTo(target + 150);
      v.playbackRate = base;
      this.debug({ targetMs: target, driftMs: drift, rate: base, action: "seek" });
    } else if (Math.abs(drift) > DEADBAND) {
      v.playbackRate = base * (drift > 0 ? 1 - NUDGE : 1 + NUDGE);
      this.debug({ targetMs: target, driftMs: drift, rate: v.playbackRate, action: "nudge" });
    } else {
      v.playbackRate = base;
      this.debug({ targetMs: target, driftMs: drift, rate: base, action: "idle" });
    }
  }

  // resume is called from a user gesture after autoplay was blocked.
  resume() {
    this.tryPlay();
  }

  private tryPlay() {
    const p = this.video.play();
    if (!p) return;
    p.then(
      () => {
        if (this.blocked) {
          this.blocked = false;
          this.onBlocked(false);
        }
      },
      (err: unknown) => {
        if (err instanceof DOMException && err.name === "NotAllowedError") {
          this.blocked = true;
          this.onBlocked(true);
        }
      },
    );
  }

  private seekTo(ms: number) {
    this.seeking = true;
    this.video.currentTime = Math.max(0, ms / 1000);
    // Guard against browsers that never fire seeked for tiny moves.
    window.setTimeout(() => {
      this.seeking = false;
    }, 1500);
  }
}
