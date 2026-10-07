import { createSignal, For, lazy, onCleanup, onMount, Show, type Component } from "solid-js";

import PlayerOverlays from "~/components/PlayerOverlays";
import PlayerTimeline from "~/components/PlayerTimeline";
import { openBugReport } from "~/lib/bugs";
import { bufferedRanges, registerProbe, registerVideo } from "~/lib/diagnostics";
import { formatTime } from "~/lib/format";
import { playerHotkey } from "~/lib/hotkeys";
import { createPlayback } from "~/lib/playback";
import type { SyncDebug } from "~/lib/sync";
import { chapterAt, chapterTarget, nextRate, RATES } from "~/lib/timeline";
import type { Chapter } from "~/protocol";
import type { RoomStore } from "~/store/room";

// Opened on demand; loaded the first time.
const HotkeysSheet = lazy(() => import("~/components/HotkeysSheet"));

type Props = {
  room: RoomStore;
  // Fullscreen is owned by the stage container so the chat overlay can
  // live inside it; the player only renders the buttons.
  onFullscreen?: () => void;
  isFullscreen?: boolean;
  // Theater widens the stage; the chat becomes an overlay (as in
  // fullscreen), so its toggle shows whenever `overlay` is set.
  isTheater?: boolean;
  onTheater?: () => void;
  overlay?: boolean;
  chatVisible?: boolean;
  onToggleChat?: () => void;
  queueVisible?: boolean;
  onToggleQueue?: () => void;
};

const SEEK_STEP_MS = 5000;
const VOLUME_STEP = 0.05;
const REACTIONS = ["👍", "❤️", "😂", "😮", "😢", "🔥", "👏", "🎉"];
const CLICK_DELAY_MS = 220; // single click waits this long for a double click

type SyncState = "ok" | "nudge" | "seek" | "off";

// Player renders the video, custom controls and the quality menu. Native
// controls are off: every interaction goes through the server so all
// viewers stay in sync; non-moderators get a read-only bar. The media
// lifecycle lives in createPlayback (lib/playback.ts), the overlays and
// the seek bar in their own components; this one owns the controls,
// preferences and input.
const Player: Component<Props> = (props) => {
  let video!: HTMLVideoElement;
  let wrap!: HTMLDivElement;
  let clickTimer: number | null = null;

  const current = () => props.room.current();
  const canControl = () => props.room.isModerator();

  const [chosen, setChosen] = createSignal<number | null>(readStored("couchcast.quality", null));
  // Subtitle language preference; "" means off. Applied when a video that
  // has the language loads, ignored otherwise.
  const [subtitle, setSubtitle] = createSignal<string>(readStored("couchcast.subtitles", ""));
  const subtitles = () => current()?.media.subtitles ?? [];
  const activeSubtitle = () => (subtitles().some((s) => s.lang === subtitle()) ? subtitle() : "");

  const playback = createPlayback(() => video, props.room, { quality: chosen, subtitle: () => activeSubtitle() || null });
  const { qualities, activeHeight, debug, nowMs, pip } = playback;

  const pickSubtitle = (lang: string) => {
    setSubtitle(lang);
    store("couchcast.subtitles", lang);
    playback.selectSubtitle(lang || null);
  };
  const toggleSubtitles = () => {
    const list = subtitles();
    if (!list.length) return;
    pickSubtitle(activeSubtitle() ? "" : list[0]!.lang);
  };
  const [showSync, setShowSync] = createSignal(false);
  const [muted, setMuted] = createSignal(readStored("couchcast.muted", true));
  const [volume, setVolume] = createSignal(readStored("couchcast.volume", 1));
  const [showKeys, setShowKeys] = createSignal(false);
  const [showReactions, setShowReactions] = createSignal(false);
  const [showChapters, setShowChapters] = createSignal(false);
  const chapters = () => current()?.media.chapters ?? [];
  const currentChapter = () => chapterAt(chapters(), nowMs());

  const seekChapter = (dir: 1 | -1) => {
    if (!canControl() || !current()) return;
    const c = chapterTarget(chapters(), nowMs(), dir);
    if (c) props.room.commands.seek(c.startMs);
  };
  const jumpToChapter = (c: Chapter) => {
    setShowChapters(false);
    if (canControl()) props.room.commands.seek(c.startMs);
  };
  const canReact = () => props.room.state.me !== null;
  const react = (emoji: string) => {
    props.room.commands.react(emoji);
    setShowReactions(false);
  };
  const pipSupported = typeof document !== "undefined" && "pictureInPictureEnabled" in document && document.pictureInPictureEnabled;
  const rate = () => props.room.state.playback?.rate || 1;
  const stepRate = (dir: 1 | -1) => {
    if (!canControl() || !current()) return;
    const next = nextRate(rate(), dir);
    if (next !== null) props.room.commands.rate(next);
  };
  const duration = () => current()?.media.durationMs ?? 0;

  // Sync state derived from the last correction, for the indicator.
  const syncState = (): SyncState => {
    const d = debug();
    if (!d || !current() || props.room.status() !== "open") return "off";
    if (d.action === "seek") return "seek";
    if (d.action === "nudge") return "nudge";
    return "ok";
  };

  onMount(() => {
    // Wheel over the video adjusts volume; the listener must not be passive
    // so the page does not scroll underneath.
    wrap.addEventListener("wheel", onWheel, { passive: false });
    onCleanup(() => wrap.removeEventListener("wheel", onWheel));

    video.muted = muted();
    video.volume = volume();

    document.addEventListener("keydown", onKey);
    onCleanup(() => document.removeEventListener("keydown", onKey));
    onCleanup(() => {
      if (clickTimer !== null) window.clearTimeout(clickTimer);
    });

    // The media half of a bug report, and the frame to attach.
    onCleanup(registerVideo(() => video));
    onCleanup(
      registerProbe("media", () => {
        const cur = current();
        const target = playback.targetMs();
        return {
          item: cur ? { id: cur.id, mediaId: cur.media.id, title: cur.media.title, source: cur.media.sourceUrl, status: cur.media.status, error: cur.media.error, durationMs: cur.media.durationMs } : null,
          loadedMediaId: playback.loadedMediaId(),
          quality: { chosen: chosen(), active: activeHeight(), available: qualities().map((q) => q.height) },
          subtitles: { selected: activeSubtitle() || null, available: subtitles().map((s) => s.lang) },
          rate: rate(),
          positionMs: Math.round(video.currentTime * 1000),
          targetMs: target === null ? null : Math.round(target),
          driftMs: target === null ? null : Math.round(video.currentTime * 1000 - target),
          element: { paused: video.paused, readyState: video.readyState, networkState: video.networkState, playbackRate: video.playbackRate, muted: video.muted, volume: video.volume, buffered: bufferedRanges(video.buffered) },
          buffering: playback.buffering(),
          autoplayBlocked: playback.blocked(),
          overlayError: playback.error(),
          pip: pip(),
          fullscreen: props.isFullscreen ?? false,
          shaka: playback.stats(),
        };
      }),
    );
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

  const pickQuality = (h: number | null) => {
    setChosen(h);
    store("couchcast.quality", h);
    playback.selectQuality(h);
  };

  const setVolumeTo = (v: number) => {
    v = Math.round(Math.max(0, Math.min(1, v)) * 100) / 100;
    video.volume = v;
    setVolume(v);
    store("couchcast.volume", v);
    if (v > 0 && video.muted) {
      video.muted = false;
      setMuted(false);
      store("couchcast.muted", false);
    }
  };

  const onWheel = (e: WheelEvent) => {
    if (!current()) return;
    e.preventDefault();
    setVolumeTo(volume() + (e.deltaY < 0 ? VOLUME_STEP : -VOLUME_STEP));
  };

  // A single click on the video toggles playback (moderators); a double
  // click toggles fullscreen without also toggling playback.
  const onVideoClick = (e: MouseEvent) => {
    if ((e.target as HTMLElement).closest("button")) return; // overlay buttons handle themselves
    if (clickTimer !== null) return;
    clickTimer = window.setTimeout(() => {
      clickTimer = null;
      togglePlay();
    }, CLICK_DELAY_MS);
  };
  const onVideoDblClick = (e: MouseEvent) => {
    if ((e.target as HTMLElement).closest("button")) return;
    if (clickTimer !== null) {
      window.clearTimeout(clickTimer);
      clickTimer = null;
    }
    fullscreen();
  };

  const togglePip = async () => {
    try {
      if (document.pictureInPictureElement) await document.exitPictureInPicture();
      else await video.requestPictureInPicture();
    } catch {
      // unsupported for this stream or denied
    }
  };

  const toggleMute = () => {
    video.muted = !video.muted;
    setMuted(video.muted);
    store("couchcast.muted", video.muted);
  };

  const onVolume = (e: Event) => setVolumeTo(Number((e.currentTarget as HTMLInputElement).value));

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

  const onKey = (e: KeyboardEvent) => {
    const action = playerHotkey(e);
    if (!action) return;
    if (showKeys()) {
      if (action === "escape" || action === "help") setShowKeys(false);
      return;
    }
    switch (action) {
      case "help":
        e.preventDefault();
        setShowKeys(true);
        break;
      case "togglePlay":
        e.preventDefault();
        togglePlay();
        break;
      case "back":
        e.preventDefault();
        seekBy(-SEEK_STEP_MS);
        break;
      case "forward":
        e.preventDefault();
        seekBy(SEEK_STEP_MS);
        break;
      case "fullscreen":
        fullscreen();
        break;
      case "theater":
        props.onTheater?.();
        break;
      case "bugReport":
        openBugReport();
        break;
      case "mute":
        toggleMute();
        break;
      case "next":
        if (canControl() && current()) props.room.commands.next();
        break;
      case "subtitles":
        toggleSubtitles();
        break;
      case "slower":
        stepRate(-1);
        break;
      case "faster":
        stepRate(1);
        break;
      case "prevChapter":
        seekChapter(-1);
        break;
      case "nextChapter":
        seekChapter(1);
        break;
    }
  };

  return (
    <div class="player">
      <div class="video-wrap" ref={wrap} onClick={onVideoClick} onDblClick={onVideoDblClick}>
        <video ref={video} playsinline />
        <PlayerOverlays room={props.room} playback={playback} canControl={canControl()} showSync={showSync()} />
      </div>

      <div class="controls">
        <div class="transport">
          <button type="button" class="icon" onClick={togglePlay} disabled={!canControl() || !current()} title={canControl() ? "Play/pause (Space)" : "Only moderators control playback"}>
            {props.room.state.playback?.playing ? "❚❚" : "▶"}
          </button>
          <span class="time">{formatTime(nowMs())}</span>
          <PlayerTimeline media={current()?.media} nowMs={nowMs()} canControl={canControl()} onSeek={(ms) => props.room.commands.seek(ms)} />
          <span class="time">{formatTime(duration())}</span>
        </div>
        <div class="control-group">
          <Show when={chapters().length > 0}>
            <div class="chapter-menu">
              <button type="button" class="chapter-btn" onClick={() => setShowChapters(!showChapters())} title="Chapters ([ ])" aria-label="Chapters" aria-expanded={showChapters()}>
                <span class="chapter-icon" aria-hidden="true">§</span>
                <span class="chapter-title">{currentChapter()?.title ?? "Chapters"}</span>
              </button>
              <Show when={showChapters()}>
                <ol class="chapter-list" role="menu">
                  <For each={chapters()}>
                    {(c) => (
                      <li>
                        <button type="button" role="menuitem" class={c === currentChapter() ? "active" : ""} onClick={() => jumpToChapter(c)} disabled={!canControl()}>
                          <span class="time">{formatTime(c.startMs)}</span>
                          <span class="chapter-name">{c.title}</span>
                        </button>
                      </li>
                    )}
                  </For>
                </ol>
              </Show>
            </div>
          </Show>
          <button type="button" class="icon" onClick={toggleMute} title="Mute (M)">
            {muted() ? "🔇" : "🔊"}
          </button>
          <input type="range" class="volume" min="0" max="1" step="0.05" value={volume()} onInput={onVolume} aria-label="Volume" />
          <select class="quality" value={chosen() === null ? "auto" : String(chosen())} onChange={(e) => pickQuality(e.currentTarget.value === "auto" ? null : Number(e.currentTarget.value))} aria-label="Quality">
            <option value="auto">Auto{activeHeight() && chosen() === null ? ` (${activeHeight()}p)` : ""}</option>
            <For each={qualities()}>{(q) => <option value={String(q.height)}>{q.height}p</option>}</For>
          </select>
          <Show when={subtitles().length > 0}>
            <select class="quality cc-select" value={activeSubtitle()} onChange={(e) => pickSubtitle(e.currentTarget.value)} aria-label="Subtitles" title="Subtitles (C)">
              <option value="">CC off</option>
              <For each={subtitles()}>
                {(s) => (
                  <option value={s.lang}>
                    {s.name || s.lang}
                    {s.auto ? " (auto)" : ""}
                  </option>
                )}
              </For>
            </select>
          </Show>
          <Show when={current()}>
            <Show
              when={canControl()}
              fallback={
                <Show when={rate() !== 1}>
                  <span class="badge rate" title="Playback speed">
                    {rate()}×
                  </span>
                </Show>
              }
            >
              <select class="quality rate-select" value={String(rate())} onChange={(e) => props.room.commands.rate(Number(e.currentTarget.value))} aria-label="Playback speed" title="Speed (< >)">
                <For each={RATES}>{(r) => <option value={String(r)}>{r}×</option>}</For>
              </select>
            </Show>
          </Show>
          <button
            type="button"
            class={`icon sync-dot ${syncState()}`}
            onClick={() => setShowSync(!showSync())}
            title={syncTitle(syncState(), debug())}
            aria-label="Sync status"
          >
            <span />
          </button>
          <Show when={props.room.state.me !== null}>
            <button type="button" class="icon dim" onClick={() => openBugReport()} title="Report a problem (Shift+B)" aria-label="Report a problem">
              ⚠
            </button>
          </Show>
          <Show when={canReact() && current()}>
            <div class="react-menu">
              <button type="button" class={`icon ${showReactions() ? "" : "dim"}`} onClick={() => setShowReactions(!showReactions())} title="React" aria-label="React" aria-expanded={showReactions()}>
                ☺
              </button>
              <Show when={showReactions()}>
                <div class="react-bar" role="menu">
                  <For each={REACTIONS}>
                    {(e) => (
                      <button type="button" class="react-btn" role="menuitem" onClick={() => react(e)}>
                        {e}
                      </button>
                    )}
                  </For>
                </div>
              </Show>
            </div>
          </Show>
          <Show when={pipSupported && current()}>
            <button type="button" class={`icon ${pip() ? "" : "dim"}`} onClick={() => void togglePip()} title={pip() ? "Leave picture-in-picture" : "Picture-in-picture"} aria-label={pip() ? "Leave picture-in-picture" : "Picture-in-picture"}>
              ▣
            </button>
          </Show>
          <Show when={props.isFullscreen && props.onToggleQueue}>
            <button type="button" class={`icon ${props.queueVisible ? "" : "dim"}`} onClick={props.onToggleQueue} title={props.queueVisible ? "Hide queue" : "Show queue"} aria-label={props.queueVisible ? "Hide queue" : "Show queue"}>
              ☰
            </button>
          </Show>
          <Show when={props.overlay && props.onToggleChat}>
            <button type="button" class={`icon ${props.chatVisible ? "" : "dim"}`} onClick={props.onToggleChat} title={props.chatVisible ? "Hide chat" : "Show chat"} aria-label={props.chatVisible ? "Hide chat" : "Show chat"}>
              💬
            </button>
          </Show>
          <Show when={props.onTheater && !props.isFullscreen}>
            <button type="button" class={`icon ${props.isTheater ? "" : "dim"}`} onClick={props.onTheater} title={props.isTheater ? "Leave theater mode (T)" : "Theater mode (T)"} aria-label={props.isTheater ? "Leave theater mode" : "Theater mode"}>
              ▭
            </button>
          </Show>
          <button type="button" class="icon" onClick={fullscreen} title={props.isFullscreen ? "Exit fullscreen (F)" : "Fullscreen (F)"} aria-label={props.isFullscreen ? "Exit fullscreen" : "Fullscreen"}>
            ⛶
          </button>
        </div>
      </div>

      <Show when={showKeys()}>
        <HotkeysSheet moderator={canControl()} onClose={() => setShowKeys(false)} />
      </Show>
    </div>
  );
};

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
