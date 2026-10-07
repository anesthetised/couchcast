import { createEffect, createSignal, on, onCleanup, onMount } from "solid-js";

import { Player as ShakaPlayer, type QualityOption } from "~/lib/player";
import { Synchronizer, type SyncDebug } from "~/lib/sync";
import type { RoomStore } from "~/store/room";

const UP_NEXT_WINDOW_MS = 5000;
const REPORT_EVERY_MS = 5000; // position heartbeat for the lag indicator

// Preferences the controller applies to each video it loads.
export type PlaybackPrefs = {
  quality: () => number | null;
  subtitle: () => string | null;
};

// createPlayback owns the media lifecycle of one <video>: the Shaka player
// and the synchronizer, loading the room's current item (and refreshing
// its token), preloading the next one, the position tick and heartbeat,
// and teardown. Call it while a component is created; video is read once
// mounted. The Player renders what it exposes.
export function createPlayback(video: () => HTMLVideoElement, room: RoomStore, prefs: PlaybackPrefs) {
  let player: ShakaPlayer | undefined;
  let sync: Synchronizer | undefined;

  const [qualities, setQualities] = createSignal<QualityOption[]>([]);
  const [activeHeight, setActiveHeight] = createSignal<number | null>(null);
  const [buffering, setBuffering] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [debug, setDebug] = createSignal<SyncDebug | null>(null);
  const [blocked, setBlocked] = createSignal(false);
  const [nowMs, setNowMs] = createSignal(0);
  const [loadedMediaId, setLoadedMediaId] = createSignal<string | null>(null);
  const [pip, setPip] = createSignal(false);

  const current = () => room.current();
  const duration = () => current()?.media.durationMs ?? 0;
  const upNext = () => {
    const list = room.state.snapshot?.queue ?? [];
    const idx = list.findIndex((q) => q.current);
    return idx >= 0 ? (list[idx + 1] ?? null) : null;
  };
  const nearEnd = () => {
    const d = duration();
    return d > 0 && room.state.playback?.playing === true && d - nowMs() <= UP_NEXT_WINDOW_MS && d - nowMs() > 0;
  };

  onMount(() => {
    const v = video();
    player = new ShakaPlayer(v);
    player.onTracks = (t, active) => {
      setQualities(t);
      setActiveHeight(active);
    };
    player.onBuffering = (b) => {
      setBuffering(b);
      room.commands.report(b ? "buffering" : "playing", v.currentTime * 1000);
    };
    player.onError = setError;
    void player.attach();

    sync = new Synchronizer(v, room.clock);
    sync.onDebug = setDebug;
    sync.onBlocked = setBlocked;
    sync.start();

    const onPlaying = () => setError(null);
    const onPipIn = () => setPip(true);
    const onPipOut = () => setPip(false);
    v.addEventListener("playing", onPlaying);
    v.addEventListener("enterpictureinpicture", onPipIn);
    v.addEventListener("leavepictureinpicture", onPipOut);

    const tick = window.setInterval(() => setNowMs(v.currentTime * 1000), 250);
    // Tell the room where this video is, so moderators see who lags.
    const heartbeat = window.setInterval(() => {
      if (loadedMediaId() && room.state.playback?.playing) {
        room.commands.report(buffering() ? "buffering" : "playing", v.currentTime * 1000);
      }
    }, REPORT_EVERY_MS);

    onCleanup(() => {
      window.clearInterval(tick);
      window.clearInterval(heartbeat);
      v.removeEventListener("playing", onPlaying);
      v.removeEventListener("enterpictureinpicture", onPipIn);
      v.removeEventListener("leavepictureinpicture", onPipOut);
      sync?.stop();
      player?.destroy();
      sync = undefined;
      player = undefined;
    });
  });

  // Load the manifest whenever the current media changes or becomes ready.
  createEffect(
    on(
      () => {
        const c = current();
        return c?.media.status === "ready" ? { id: c.media.id, manifest: c.media.manifest, token: c.media.token } : null;
      },
      async (target) => {
        const p = player;
        const s = sync;
        if (!p || !s) return;
        if (!target || !target.manifest || !target.token) {
          if (loadedMediaId() !== null) {
            setLoadedMediaId(null);
            await p.unload();
          }
          return;
        }
        if (target.id === loadedMediaId()) {
          p.setTokenFor(target.manifest, target.token);
          return;
        }
        s.suspended = true;
        setError(null);
        try {
          // The effect that feeds the synchronizer runs after this one, so
          // on the first load it would not know the room's position yet
          // and every viewer would start at 0 and then seek.
          const pb = room.state.playback;
          if (pb) s.update({ ...pb });
          await p.load(target.manifest, target.token, s.targetMs() ?? 0);
          setLoadedMediaId(target.id);
          p.selectQuality(prefs.quality());
          const subs = current()?.media.subtitles ?? [];
          if (subs.length) {
            await p.addSubtitles(target.manifest, subs);
            p.selectSubtitle(prefs.subtitle());
          }
        } catch (e) {
          const code = (e as { code?: number }).code;
          // 7000 = LOAD_INTERRUPTED: a newer load superseded this one.
          if (code !== 7000) setError(code ? `Player error ${code}` : e instanceof Error ? e.message : String(e));
        } finally {
          s.suspended = false;
        }
      },
    ),
  );

  // Preload the next item's manifest shortly before the current one ends
  // so auto-advance starts without a black gap.
  createEffect(() => {
    const next = upNext();
    if (!player || !nearEnd() || !next || next.media.status !== "ready" || !next.media.manifest || !next.media.token) return;
    void player.preload(next.media.manifest, next.media.token);
  });

  // Feed playback updates to the synchronizer.
  createEffect(() => {
    const pb = room.state.playback;
    if (pb && sync) sync.update({ ...pb });
  });

  return {
    qualities,
    activeHeight,
    buffering,
    error,
    debug,
    blocked,
    nowMs,
    loadedMediaId,
    pip,
    upNext,
    nearEnd,
    selectQuality: (h: number | null) => player?.selectQuality(h),
    selectSubtitle: (lang: string | null) => player?.selectSubtitle(lang),
    // resume runs inside the user's gesture, which is what the autoplay
    // policy wants.
    resume: () => sync?.resume(),
    targetMs: () => sync?.targetMs() ?? null,
    stats: () => player?.stats() ?? null,
  };
}

export type Playback = ReturnType<typeof createPlayback>;
