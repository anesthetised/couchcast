import { createSignal, onCleanup, Show, type Component } from "solid-js";

import { formatAgo, formatTime } from "~/lib/format";
import { Player } from "~/lib/player";
import { rooms } from "~/lib/rooms";
import { toast } from "~/lib/toast";
import type { DirectoryRoom } from "~/lib/types";
import { auth } from "~/store/auth";

type Props = {
  room: DirectoryRoom;
  // serverOffsetMs = serverNow − localNow, from the directory response.
  serverOffsetMs: number;
};

const HOVER_DELAY_MS = 300;
const PREVIEW_MAX_HEIGHT = 360;

// Only one preview plays at a time: hovering a second card tears down the
// first, so a sweep across the grid never spawns a pile of players.
let activePreview: (() => void) | null = null;

const canHover = () => window.matchMedia("(hover: hover)").matches;

// RoomCard shows what a public room is playing; hovering plays a muted
// preview from the room's current position.
const RoomCard: Component<Props> = (props) => {
  let video: HTMLVideoElement | undefined;
  let player: Player | undefined;
  let timer: number | null = null;
  const [previewing, setPreviewing] = createSignal(false);

  const media = () => props.room.media;
  const playable = () => Boolean(media()?.manifest && media()?.token);

  // Position the room is at right now, in ms.
  const currentPositionMs = () => {
    const pb = props.room.playback;
    if (!pb) return 0;
    if (!pb.playing) return pb.positionMs;
    return pb.positionMs + (Date.now() + props.serverOffsetMs - pb.atServerMs) * (pb.rate || 1);
  };

  const stopPreview = () => {
    if (timer !== null) {
      window.clearTimeout(timer);
      timer = null;
    }
    if (activePreview === stopPreview) activePreview = null;
    player?.destroy();
    player = undefined;
    setPreviewing(false);
  };

  const startPreview = async () => {
    const m = media();
    if (!video || !m?.manifest || !m.token) return;
    activePreview?.();
    activePreview = stopPreview;

    player = new Player(video, { maxHeight: PREVIEW_MAX_HEIGHT });
    video.muted = true;
    try {
      await player.attach();
      await player.load(m.manifest, m.token, currentPositionMs());
      if (activePreview !== stopPreview) return; // torn down while loading
      setPreviewing(true);
      if (props.room.playback?.playing) await video.play().catch(() => {});
    } catch {
      stopPreview();
    }
  };

  const onEnter = () => {
    if (!canHover() || !playable()) return;
    timer = window.setTimeout(() => {
      timer = null;
      void startPreview();
    }, HOVER_DELAY_MS);
  };

  onCleanup(stopPreview);

  // The star is optimistic and per user; the card itself is a link, so
  // the button lives next to it rather than inside.
  const [starred, setStarred] = createSignal(props.room.starred);
  const toggleStar = async () => {
    const next = !starred();
    setStarred(next);
    try {
      await rooms.star(props.room.slug, next);
    } catch (err) {
      setStarred(!next);
      toast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  const status = () => {
    if (!media()) return null;
    if (!playable()) return "preparing…";
    if (props.room.playback && !props.room.playback.playing) return "paused";
    return null;
  };

  return (
    <div class="room-card-wrap">
      <a class="room-card" href={`/r/${props.room.slug}`} onMouseEnter={onEnter} onMouseLeave={stopPreview}>
        <div class="room-card-media">
          <Show
            when={media()?.thumbnailUrl}
            fallback={
              <div class="room-card-empty">
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true">
                  <rect x="3" y="5" width="18" height="14" rx="2" />
                  <path d="M10 9.5v5l4.5-2.5z" fill="currentColor" stroke="none" />
                </svg>
                <Show when={!media()}>Nothing playing</Show>
              </div>
            }
          >
            {(src) => <img src={src()} alt="" loading="lazy" />}
          </Show>
          <video ref={video} muted playsinline autoplay={props.room.playback?.playing ?? false} classList={{ visible: previewing() }} />
          <div class="room-card-top">
            <span class="actions" style={{ gap: "0.3rem" }}>
              <Show when={props.room.live}>
                <span class="badge live">live</span>
              </Show>
              <Show when={status()}>{(s) => <span class="badge">{s()}</span>}</Show>
            </span>
            <span class="actions" style={{ gap: "0.3rem" }}>
              <Show when={props.room.myRole && props.room.myRole !== "member"}>
                <span class="badge role">{props.room.myRole === "moderator" ? "mod" : props.room.myRole}</span>
              </Show>
              <Show when={props.room.visibility === "private"}>
                <span class="badge private">🔒 private</span>
              </Show>
            </span>
          </div>
          <Show when={media()?.durationMs}>{(d) => <span class="room-card-duration">{formatTime(d())}</span>}</Show>
        </div>
        <div class="room-card-body">
          <div class="room-card-title">{media()?.title || props.room.name}</div>
          <Show when={props.room.description}>{(d) => <div class="room-card-desc">{d()}</div>}</Show>
          <div class="room-card-meta">
            <Show when={media()}>
              <span>{props.room.name}</span>
              <span>·</span>
            </Show>
            <span>{props.room.owner}</span>
            <span>·</span>
            <Show when={media() || props.room.viewers > 0} fallback={<span title="Last activity">active {formatAgo(props.room.lastActiveMs)}</span>}>
              <span>{props.room.viewers} watching</span>
            </Show>
          </div>
        </div>
      </a>
      <Show when={auth.user()}>
        <button type="button" class={`star ${starred() ? "on" : ""}`} onClick={() => void toggleStar()} title={starred() ? "Unstar" : "Star this room"} aria-pressed={starred()}>
          {starred() ? "★" : "☆"}
        </button>
      </Show>
    </div>
  );
};

export default RoomCard;
