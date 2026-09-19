import { createEffect, createSignal, on, onCleanup, onMount, Show, For, type Component } from "solid-js";

import { formatTime } from "~/lib/format";
import { Player as ShakaPlayer, type QualityOption } from "~/lib/player";
import { Synchronizer, type SyncDebug } from "~/lib/sync";
import type { RoomStore } from "~/store/room";

type Props = { room: RoomStore };

// Player renders the video, custom controls and the quality menu. Native
// controls are off: every interaction goes through the server so all
// viewers stay in sync; non-moderators get a read-only bar.
const Player: Component<Props> = (props) => {
  let video!: HTMLVideoElement;
  let player: ShakaPlayer | undefined;
  let sync: Synchronizer | undefined;

  const [qualities, setQualities] = createSignal<QualityOption[]>([]);
  const [activeHeight, setActiveHeight] = createSignal<number | null>(null);
  const [chosen, setChosen] = createSignal<number | null>(readStoredQuality());
  const [buffering, setBuffering] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [debug, setDebug] = createSignal<SyncDebug | null>(null);
  const [showDebug, setShowDebug] = createSignal(false);
  const [nowMs, setNowMs] = createSignal(0);
  const [muted, setMuted] = createSignal(true);
  const [volume, setVolume] = createSignal(1);
  const [loadedMediaId, setLoadedMediaId] = createSignal<string | null>(null);

  const current = () => props.room.current();
  const canControl = () => props.room.isModerator();
  const duration = () => current()?.media.durationMs ?? 0;

  onMount(() => {
    player = new ShakaPlayer(video);
    player.onTracks = (t, active) => {
      setQualities(t);
      setActiveHeight(active);
    };
    player.onBuffering = (b) => {
      setBuffering(b);
      props.room.commands.report(b ? "buffering" : "playing", video.currentTime * 1000);
    };
    player.onError = (m) => setError(m);
    void player.attach();

    sync = new Synchronizer(video, props.room.clock);
    sync.onDebug = setDebug;
    sync.start();

    video.muted = true;
    const tick = window.setInterval(() => setNowMs(video.currentTime * 1000), 250);
    onCleanup(() => window.clearInterval(tick));
  });

  onCleanup(() => {
    sync?.stop();
    player?.destroy();
  });

  // Load the manifest whenever the current media changes or becomes ready.
  createEffect(
    on(
      () => {
        const c = current();
        return c?.media.status === "ready" ? { id: c.media.id, manifest: c.media.manifest, token: c.media.token } : null;
      },
      async (target) => {
        if (!player || !sync) return;
        if (!target || !target.manifest || !target.token) {
          if (loadedMediaId() !== null) {
            setLoadedMediaId(null);
            await player.unload();
          }
          return;
        }
        if (target.id === loadedMediaId()) {
          player.setToken(target.token);
          return;
        }
        sync.suspended = true;
        setError(null);
        try {
          const start = sync.targetMs() ?? 0;
          await player.load(target.manifest, target.token, start);
          setLoadedMediaId(target.id);
          player.selectQuality(chosen());
        } catch (e) {
          setError(e instanceof Error ? e.message : String(e));
        } finally {
          sync.suspended = false;
        }
      },
    ),
  );

  // Feed playback updates to the synchroniser.
  createEffect(() => {
    const pb = props.room.state.playback;
    if (pb && sync) sync.update({ ...pb });
  });

  const togglePlay = () => {
    if (!canControl()) return;
    if (props.room.state.playback?.playing) props.room.commands.pause();
    else props.room.commands.play();
  };

  const onSeekInput = (e: Event) => {
    if (!canControl()) return;
    const value = Number((e.currentTarget as HTMLInputElement).value);
    props.room.commands.seek(value);
  };

  const pickQuality = (h: number | null) => {
    setChosen(h);
    storeQuality(h);
    player?.selectQuality(h);
  };

  const toggleMute = () => {
    video.muted = !video.muted;
    setMuted(video.muted);
  };

  const onVolume = (e: Event) => {
    const v = Number((e.currentTarget as HTMLInputElement).value);
    video.volume = v;
    setVolume(v);
    if (v > 0 && video.muted) {
      video.muted = false;
      setMuted(false);
    }
  };

  const fullscreen = () => {
    const el = video.parentElement;
    if (!el) return;
    if (document.fullscreenElement) void document.exitFullscreen();
    else void el.requestFullscreen();
  };

  return (
    <div class="player">
      <div class="video-wrap" onDblClick={fullscreen}>
        <video ref={video} playsinline />
        <Show when={!current()}>
          <div class="video-overlay muted">Queue is empty — add a link to start.</div>
        </Show>
        <Show when={current() && current()!.media.status !== "ready"}>
          <div class="video-overlay">
            <p>{statusLabel(current()!.media.status)}</p>
            <Show when={current()!.media.status === "downloading"}>
              <progress max="1" value={current()!.media.progress} />
            </Show>
            <Show when={current()!.media.error}>
              <p class="error">{current()!.media.error}</p>
            </Show>
          </div>
        </Show>
        <Show when={buffering() && current()?.media.status === "ready"}>
          <div class="video-overlay spinner">buffering…</div>
        </Show>
        <Show when={error()}>{(e) => <div class="video-overlay error">{e()}</div>}</Show>
        <Show when={showDebug() && debug()}>
          {(d) => (
            <pre class="debug-overlay">
              offset {props.room.clockInfo().offset.toFixed(0)}ms rtt {props.room.clockInfo().rtt.toFixed(0)}ms{"\n"}
              drift {d().driftMs.toFixed(0)}ms rate {d().rate.toFixed(2)} {d().action}{"\n"}
              seq {props.room.state.playback?.seq ?? 0} active {activeHeight() ?? "-"}p
            </pre>
          )}
        </Show>
      </div>

      <div class="controls">
        <button type="button" class="icon" onClick={togglePlay} disabled={!canControl() || !current()} title={canControl() ? "" : "Only moderators control playback"}>
          {props.room.state.playback?.playing ? "❚❚" : "▶"}
        </button>
        <span class="time">{formatTime(nowMs())}</span>
        <input
          type="range"
          class="seek"
          min="0"
          max={duration() || 0}
          value={Math.min(nowMs(), duration() || 0)}
          disabled={!canControl() || !duration()}
          onChange={onSeekInput}
        />
        <span class="time">{formatTime(duration())}</span>
        <button type="button" class="icon" onClick={toggleMute} title="Mute">
          {muted() ? "🔇" : "🔊"}
        </button>
        <input type="range" class="volume" min="0" max="1" step="0.05" value={volume()} onInput={onVolume} />
        <select class="quality" value={chosen() === null ? "auto" : String(chosen())} onChange={(e) => pickQuality(e.currentTarget.value === "auto" ? null : Number(e.currentTarget.value))}>
          <option value="auto">Auto{activeHeight() && chosen() === null ? ` (${activeHeight()}p)` : ""}</option>
          <For each={qualities()}>{(q) => <option value={String(q.height)}>{q.height}p</option>}</For>
        </select>
        <button type="button" class="icon" onClick={() => setShowDebug(!showDebug())} title="Sync debug">
          ⓘ
        </button>
        <button type="button" class="icon" onClick={fullscreen} title="Fullscreen">
          ⛶
        </button>
      </div>
    </div>
  );
};

function statusLabel(status: string): string {
  switch (status) {
    case "queued":
      return "Waiting for the ingest worker…";
    case "probing":
      return "Looking up the video…";
    case "downloading":
      return "Downloading…";
    case "packaging":
      return "Packaging…";
    case "uploading":
      return "Uploading…";
    case "failed":
      return "Ingest failed";
    default:
      return status;
  }
}

const QUALITY_KEY = "couchcast.quality";

function readStoredQuality(): number | null {
  try {
    const v = localStorage.getItem(QUALITY_KEY);
    return v ? Number(v) : null;
  } catch {
    return null;
  }
}

function storeQuality(h: number | null) {
  try {
    if (h === null) localStorage.removeItem(QUALITY_KEY);
    else localStorage.setItem(QUALITY_KEY, String(h));
  } catch {
    // storage unavailable
  }
}

export default Player;
