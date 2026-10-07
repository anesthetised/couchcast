import { For, Show, type Component } from "solid-js";

import { openBugReport } from "~/lib/bugs";
import { progressDetail } from "~/lib/format";
import type { Playback } from "~/lib/playback";
import type { RoomStore } from "~/store/room";

type Props = {
  room: RoomStore;
  playback: Playback;
  canControl: boolean;
  showSync: boolean;
};

// PlayerOverlays draws everything layered over the video: the autoplay
// gate, idle and preparing states, the countdown, the buffering wait,
// errors, the up-next card, reactions and the sync debug overlay.
const PlayerOverlays: Component<Props> = (props) => {
  const current = () => props.room.current();
  const pb = () => props.playback;
  // Whole seconds left of a counted-down start, by the server clock; the
  // position tick keeps it moving between snapshots.
  const countdownLeft = () => {
    const at = props.room.state.snapshot?.countdownMs;
    pb().nowMs();
    if (!at) return null;
    const left = Math.ceil((at - props.room.clock.serverNow()) / 1000);
    return left > 0 ? left : null;
  };

  return (
    <>
      <Show when={pb().blocked() && current()?.media.status === "ready"}>
        <button type="button" class="video-overlay gate" onClick={() => pb().resume()}>
          <span class="gate-icon" aria-hidden="true">▶</span>
          <span>Tap to play</span>
        </button>
      </Show>

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
            <Show when={progressDetail(current()!.media)}>
              {(detail) => (
                <>
                  <progress max="1" value={current()!.media.progress} />
                  <span class="muted small">{detail()}</span>
                </>
              )}
            </Show>
            <Show when={current()!.media.error}>
              <p class="error">{current()!.media.error}</p>
            </Show>
          </div>
        </div>
      </Show>

      <Show when={countdownLeft()}>
        {(n) => (
          <div class="video-overlay countdown" role="status" aria-live="assertive">
            <span class="countdown-number">{n()}</span>
          </div>
        )}
      </Show>

      <Show when={(props.room.state.snapshot?.waiting ?? []).length > 0 && current()}>
        <div class="video-overlay waiting" role="status">
          <span class="ring" aria-hidden="true" />
          <span>Waiting for {listNames(props.room.state.snapshot?.waiting ?? [])}…</span>
          <Show when={props.canControl}>
            <button type="button" class="ghost small" onClick={() => props.room.commands.play()}>
              Continue without them
            </button>
          </Show>
        </div>
      </Show>

      <Show when={pb().buffering() && !pb().blocked() && current()?.media.status === "ready"}>
        <div class="video-overlay spinner" role="status" aria-label="Buffering">
          <span class="ring" />
        </div>
      </Show>
      <Show when={pb().error()}>
        {(e) => (
          <div class="video-overlay error">
            <span>{e()}</span>
            <button type="button" class="ghost small" onClick={() => openBugReport({ category: "playback", description: `The player stopped with “${e()}”.` })}>
              Report this
            </button>
          </div>
        )}
      </Show>

      <Show when={pb().nearEnd() && pb().upNext()}>
        {(next) => (
          <div class="up-next-card">
            <span class="muted small">Up next</span>
            <strong>{next().media.title || next().media.sourceUrl}</strong>
            <Show when={props.canControl}>
              <button type="button" class="ghost" onClick={() => props.room.commands.next()}>
                Play now
              </button>
            </Show>
          </div>
        )}
      </Show>

      <div class="reactions" aria-hidden="true">
        <For each={props.room.reactions()}>
          {(r) => (
            <span class="reaction" style={{ left: `${10 + ((r.id * 37) % 80)}%` }} title={r.username}>
              {r.emoji}
            </span>
          )}
        </For>
      </div>

      <Show when={props.showSync && pb().debug()}>
        {(d) => (
          <pre class="debug-overlay">
            offset {props.room.clockInfo().offset.toFixed(0)}ms rtt {props.room.clockInfo().rtt.toFixed(0)}ms{"\n"}
            drift {d().driftMs.toFixed(0)}ms rate {d().rate.toFixed(2)} {d().action}{"\n"}
            seq {props.room.state.playback?.seq ?? 0} active {pb().activeHeight() ?? "-"}p
          </pre>
        )}
      </Show>
    </>
  );
};

function listNames(names: string[]): string {
  if (names.length <= 2) return names.join(" and ");
  return `${names.slice(0, 2).join(", ")} and ${names.length - 2} more`;
}

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

export default PlayerOverlays;
