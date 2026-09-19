import { createSignal, onCleanup, Show, type Component } from "solid-js";

import { formatTime } from "~/lib/format";
import { Player } from "~/lib/player";
import type { DirectoryRoom } from "~/lib/types";

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
    return pb.positionMs + (Date.now() + props.serverOffsetMs - pb.atServerMs);
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

  return (
    <a class="room-card" href={`/r/${props.room.slug}`} onMouseEnter={onEnter} onMouseLeave={stopPreview}>
      <div class="room-card-media">
        <Show when={media()?.thumbnailUrl} fallback={<div class="room-card-empty">{media() ? "" : "Nothing playing"}</div>}>
          {(src) => <img src={src()} alt="" loading="lazy" />}
        </Show>
        <video ref={video} muted playsinline autoplay={props.room.playback?.playing ?? false} classList={{ visible: previewing() }} />
        <Show when={props.room.live}>
          <span class="room-card-live">● live</span>
        </Show>
        <Show when={props.room.viewers > 0}>
          <span class="room-card-viewers">👁 {props.room.viewers}</span>
        </Show>
        <Show when={media() && props.room.playback && !props.room.playback.playing}>
          <span class="room-card-badge">paused</span>
        </Show>
        <Show when={media() && !playable()}>
          <span class="room-card-badge">preparing…</span>
        </Show>
        <Show when={media()?.durationMs}>{(d) => <span class="room-card-duration">{formatTime(d())}</span>}</Show>
      </div>
      <div class="room-card-body">
        <div class="room-card-title">{media()?.title || props.room.name}</div>
        <div class="muted small">
          <Show when={media()}>{props.room.name} · </Show>
          by {props.room.owner} · {props.room.memberCount} member{props.room.memberCount === 1 ? "" : "s"}
        </div>
      </div>
    </a>
  );
};

export default RoomCard;
