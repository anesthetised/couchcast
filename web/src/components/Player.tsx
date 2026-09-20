import { createEffect, createSignal, on, onCleanup, onMount, Show, For, type Component } from "solid-js";

import { formatTime } from "~/lib/format";
import { Player as ShakaPlayer, type QualityOption } from "~/lib/player";
import { Synchronizer, type SyncDebug } from "~/lib/sync";
import type { RoomStore } from "~/store/room";

type Props = {
  room: RoomStore;
  // Fullscreen is owned by the stage container so the chat overlay can
  // live inside it; the player only renders the buttons.
  onFullscreen?: () => void;
  isFullscreen?: boolean;
  chatVisible?: boolean;
  onToggleChat?: () => void;
  queueVisible?: boolean;
  onToggleQueue?: () => void;
};

const UP_NEXT_WINDOW_MS = 5000;
const SEEK_STEP_MS = 5000;

type SyncState = "ok" | "nudge" | "seek" | "off";

// Player renders the video, custom controls and the quality menu. Native
// controls are off: every interaction goes through the server so all
// viewers stay in sync; non-moderators get a read-only bar.
const Player: Component<Props> = (props) => {
  let video!: HTMLVideoElement;
  let player: ShakaPlayer | undefined;
  let sync: Synchronizer | undefined;

  const [qualities, setQualities] = createSignal<QualityOption[]>([]);
  const [activeHeight, setActiveHeight] = createSignal<number | null>(null);
  const [chosen, setChosen] = createSignal<number | null>(readStored("couchcast.quality", null));
  const [buffering, setBuffering] = createSignal(false);
  const [error, setError] = createSignal<string | null>(null);
  const [debug, setDebug] = createSignal<SyncDebug | null>(null);
  const [showSync, setShowSync] = createSignal(false);
  const [nowMs, setNowMs] = createSignal(0);
  const [muted, setMuted] = createSignal(readStored("couchcast.muted", true));
  const [volume, setVolume] = createSignal(readStored("couchcast.volume", 1));
  const [loadedMediaId, setLoadedMediaId] = createSignal<string | null>(null);

  const current = () => props.room.current();
  const canControl = () => props.room.isModerator();
  const duration = () => current()?.media.durationMs ?? 0;
  const queue = () => props.room.state.snapshot?.queue ?? [];
  const upNext = () => {
    const list = queue();
    const idx = list.findIndex((q) => q.current);
    return idx >= 0 ? (list[idx + 1] ?? null) : null;
  };
  const nearEnd = () => {
    const d = duration();
    return d > 0 && props.room.state.playback?.playing === true && d - nowMs() <= UP_NEXT_WINDOW_MS && d - nowMs() > 0;
  };

  // Sync state derived from the last correction, for the indicator.
  const syncState = (): SyncState => {
    const d = debug();
    if (!d || !current() || props.room.status() !== "open") return "off";
    if (d.action === "seek") return "seek";
    if (d.action === "nudge") return "nudge";
    return "ok";
  };

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

    video.muted = muted();
    video.volume = volume();
    const tick = window.setInterval(() => setNowMs(video.currentTime * 1000), 250);
    onCleanup(() => window.clearInterval(tick));

    document.addEventListener("keydown", onKey);
    onCleanup(() => document.removeEventListener("keydown", onKey));
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
    if (!canControl() || !current()) return;
    if (props.room.state.playback?.playing) props.room.commands.pause();
    else props.room.commands.play();
  };

  const seekBy = (deltaMs: number) => {
    if (!canControl() || !current()) return;
    const target = Math.max(0, Math.min(duration() || Infinity, nowMs() + deltaMs));
    props.room.commands.seek(target);
  };

  const onSeekInput = (e: Event) => {
    if (!canControl()) return;
    props.room.commands.seek(Number((e.currentTarget as HTMLInputElement).value));
  };

  const pickQuality = (h: number | null) => {
    setChosen(h);
    store("couchcast.quality", h);
    player?.selectQuality(h);
  };

  const toggleMute = () => {
    video.muted = !video.muted;
    setMuted(video.muted);
    store("couchcast.muted", video.muted);
  };

  const onVolume = (e: Event) => {
    const v = Number((e.currentTarget as HTMLInputElement).value);
    video.volume = v;
    setVolume(v);
    store("couchcast.volume", v);
    if (v > 0 && video.muted) {
      video.muted = false;
      setMuted(false);
      store("couchcast.muted", false);
    }
  };

  const fullscreen = () => {
    if (props.onFullscreen) {
      props.onFullscreen();
      return;
    }
    const el = video.parentElement;
    if (!el) return;
    if (document.fullscreenElement) void document.exitFullscreen();
    else void el.requestFullscreen();
  };

  // Hotkeys: ignored while typing so the chat stays usable.
  const onKey = (e: KeyboardEvent) => {
    const t = e.target as HTMLElement | null;
    if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
    if (e.metaKey || e.ctrlKey || e.altKey) return;
    switch (e.key) {
      case " ":
        e.preventDefault();
        togglePlay();
        break;
      case "ArrowLeft":
        e.preventDefault();
        seekBy(-SEEK_STEP_MS);
        break;
      case "ArrowRight":
        e.preventDefault();
        seekBy(SEEK_STEP_MS);
        break;
      case "f":
      case "F":
        fullscreen();
        break;
      case "m":
      case "M":
        toggleMute();
        break;
      case "n":
      case "N":
        if (canControl() && upNext()) props.room.commands.next();
        break;
    }
  };

  return (
    <div class="player">
      <div class="video-wrap" onDblClick={fullscreen}>
        <video ref={video} playsinline />

        <Show when={!current()}>
          <div class="video-overlay quiet">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
              <rect x="3" y="5" width="18" height="14" rx="2" />
              <path d="M10 9.5v5l4.5-2.5z" fill="currentColor" stroke="none" />
            </svg>
            <span>Nothing playing</span>
          </div>
        </Show>

        <Show when={current() && current()!.media.status !== "ready"}>
          <div class="video-overlay preparing">
            <Show when={current()!.media.thumbnailUrl}>{(src) => <img class="poster" src={src()} alt="" />}</Show>
            <div class="preparing-body">
              <p>{statusLabel(current()!.media.status)}</p>
              <Show when={current()!.media.status === "downloading"}>
                <progress max="1" value={current()!.media.progress} />
              </Show>
              <Show when={current()!.media.error}>
                <p class="error">{current()!.media.error}</p>
              </Show>
            </div>
          </div>
        </Show>

        <Show when={buffering() && current()?.media.status === "ready"}>
          <div class="video-overlay spinner">buffering…</div>
        </Show>
        <Show when={error()}>{(e) => <div class="video-overlay error">{e()}</div>}</Show>

        <Show when={nearEnd() && upNext()}>
          {(next) => (
            <div class="up-next-card">
              <span class="muted small">Up next</span>
              <strong>{next().media.title || next().media.sourceUrl}</strong>
              <Show when={canControl()}>
                <button type="button" class="ghost" onClick={() => props.room.commands.next()}>
                  Play now
                </button>
              </Show>
            </div>
          )}
        </Show>

        <Show when={showSync() && debug()}>
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
        <button type="button" class="icon" onClick={togglePlay} disabled={!canControl() || !current()} title={canControl() ? "Play/pause (Space)" : "Only moderators control playback"}>
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
          aria-label="Position"
        />
        <span class="time">{formatTime(duration())}</span>
        <button type="button" class="icon" onClick={toggleMute} title="Mute (M)">
          {muted() ? "🔇" : "🔊"}
        </button>
        <input type="range" class="volume" min="0" max="1" step="0.05" value={volume()} onInput={onVolume} aria-label="Volume" />
        <select class="quality" value={chosen() === null ? "auto" : String(chosen())} onChange={(e) => pickQuality(e.currentTarget.value === "auto" ? null : Number(e.currentTarget.value))} aria-label="Quality">
          <option value="auto">Auto{activeHeight() && chosen() === null ? ` (${activeHeight()}p)` : ""}</option>
          <For each={qualities()}>{(q) => <option value={String(q.height)}>{q.height}p</option>}</For>
        </select>
        <button
          type="button"
          class={`icon sync-dot ${syncState()}`}
          onClick={() => setShowSync(!showSync())}
          title={syncTitle(syncState(), debug())}
          aria-label="Sync status"
        >
          <span />
        </button>
        <Show when={props.isFullscreen && props.onToggleQueue}>
          <button type="button" class={`icon ${props.queueVisible ? "" : "dim"}`} onClick={props.onToggleQueue} title={props.queueVisible ? "Hide queue" : "Show queue"}>
            ☰
          </button>
        </Show>
        <Show when={props.isFullscreen && props.onToggleChat}>
          <button type="button" class={`icon ${props.chatVisible ? "" : "dim"}`} onClick={props.onToggleChat} title={props.chatVisible ? "Hide chat" : "Show chat"}>
            💬
          </button>
        </Show>
        <button type="button" class="icon" onClick={fullscreen} title={props.isFullscreen ? "Exit fullscreen (F)" : "Fullscreen (F)"}>
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

function syncTitle(state: SyncState, d: SyncDebug | null): string {
  const drift = d ? ` · drift ${Math.round(d.driftMs)} ms` : "";
  switch (state) {
    case "ok":
      return `In sync${drift}`;
    case "nudge":
      return `Catching up${drift}`;
    case "seek":
      return `Resyncing${drift}`;
    default:
      return "Not connected";
  }
}

function readStored<T>(key: string, fallback: T): T {
  try {
    const v = localStorage.getItem(key);
    return v === null ? fallback : (JSON.parse(v) as T);
  } catch {
    return fallback;
  }
}

function store(key: string, value: unknown) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // storage unavailable
  }
}

export default Player;
