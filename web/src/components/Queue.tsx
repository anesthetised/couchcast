import { createSignal, For, onCleanup, Show, type Component } from "solid-js";

import ReportDialog from "~/components/ReportDialog";
import { formatDuration, formatTime } from "~/lib/format";
import type { RoomStore } from "~/store/room";
import type { QueueEntry } from "~/protocol";

type Props = { room: RoomStore };

const Queue: Component<Props> = (props) => {
  const items = () => props.room.state.snapshot?.queue ?? [];
  const played = () => props.room.state.snapshot?.played ?? [];
  const pending = () => props.room.pending();
  const canAdd = () => canManage() || (me() !== null && (props.room.state.snapshot?.room.settings.viewersCanAdd ?? false));
  const canManage = () => props.room.isModerator();
  const me = () => props.room.state.me;
  const voteMode = () => props.room.state.snapshot?.room.settings.voteMode ?? false;
  const [reporting, setReporting] = createSignal<QueueEntry | null>(null);
  const waiting = () => items().filter((q) => !q.current).length;
  const clearQueue = () => {
    if (!confirm(`Remove ${waiting()} waiting ${waiting() === 1 ? "video" : "videos"} from the queue?`)) return;
    props.room.commands.clear();
  };

  // Time left in the whole queue: the rest of the current item plus every
  // item after it. Ticks coarsely; the header only shows minutes.
  const [tick, setTick] = createSignal(0);
  const ticker = window.setInterval(() => setTick((t) => t + 1), 10_000);
  onCleanup(() => window.clearInterval(ticker));
  const remainingMs = () => {
    tick();
    const list = items();
    const idx = list.findIndex((q) => q.current);
    let total = 0;
    if (idx >= 0) {
      const pb = props.room.state.playback;
      const cur = list[idx]!;
      let pos = pb?.positionMs ?? 0;
      if (pb?.playing) pos += (props.room.clock.serverNow() - pb.atServerMs) * pb.rate;
      total += Math.max(0, cur.media.durationMs - pos);
    }
    for (const q of list.slice(idx + 1)) total += q.media.durationMs;
    return total;
  };

  // Pointer drag-and-drop in manual mode. The current item is pinned; a
  // drop maps to queue.move with the item above the target as the anchor.
  const canDrag = () => canManage() && !voteMode();
  const [dragging, setDragging] = createSignal<string | null>(null);
  const [over, setOver] = createSignal<string | null>(null);

  const onDragStart = (e: DragEvent, item: QueueEntry) => {
    if (!canDrag() || item.current) return e.preventDefault();
    setDragging(item.id);
    e.dataTransfer?.setData("text/plain", item.id);
    if (e.dataTransfer) e.dataTransfer.effectAllowed = "move";
  };
  const onDragOver = (e: DragEvent, item: QueueEntry) => {
    if (!dragging() || item.current) return;
    e.preventDefault();
    setOver(item.id);
  };
  const onDrop = (e: DragEvent, target: QueueEntry) => {
    e.preventDefault();
    const id = dragging();
    setDragging(null);
    setOver(null);
    if (!id || id === target.id || target.current) return;
    const list = items();
    const from = list.findIndex((q) => q.id === id);
    const to = list.findIndex((q) => q.id === target.id);
    if (from < 0 || to < 0) return;
    // Moving down: land after the target; moving up: land before it.
    const anchor = from < to ? target : list[to - 1];
    props.room.commands.move(id, anchor && !anchor.current ? anchor.id : anchor?.current ? anchor.id : null);
  };
  const onDragEnd = () => {
    setDragging(null);
    setOver(null);
  };

  const moveUp = (idx: number) => {
    const list = items();
    const item = list[idx];
    if (!item || idx < 1) return;
    // Place after the item two positions up (null = head).
    const anchor = idx >= 2 ? list[idx - 2] : null;
    props.room.commands.move(item.id, anchor ? anchor.id : null);
  };

  const moveDown = (idx: number) => {
    const list = items();
    const item = list[idx];
    const next = list[idx + 1];
    if (!item || !next) return;
    props.room.commands.move(item.id, next.id);
  };

  return (
    <section class="queue">
      <h2 class="section-title">
        Up next <span class="muted">{items().length}</span>
        <Show when={remainingMs() > 0}>
          <span class="muted queue-total" title="Time left in the queue">
            · {formatDuration(remainingMs())}
          </span>
        </Show>
        <Show when={canManage() && waiting() >= 2}>
          <span class="section-actions">
            <Show when={!voteMode()}>
              <button type="button" class="link small" onClick={() => props.room.commands.shuffle()} title="Reorder the waiting videos at random">
                Shuffle
              </button>
            </Show>
            <button type="button" class="link small danger-text" onClick={clearQueue} title="Remove every waiting video">
              Clear
            </button>
          </span>
        </Show>
      </h2>
      <Show when={items().length === 0 && pending().length === 0}>
        <p class="muted small queue-empty">Nothing queued yet.</p>
      </Show>
      <ul class="list">
        <For each={items()}>
          {(item, idx) => (
            <li
              class={`queue-item ${item.current ? "current" : ""} ${dragging() === item.id ? "dragging" : ""} ${over() === item.id ? "over" : ""}`}
              draggable={canDrag() && !item.current}
              onDragStart={(e) => onDragStart(e, item)}
              onDragOver={(e) => onDragOver(e, item)}
              onDrop={(e) => onDrop(e, item)}
              onDragEnd={onDragEnd}
            >
              <div class="thumb">
                <Show when={item.media.thumbnailUrl} fallback={<div class="thumb-empty" />}>
                  <img src={item.media.thumbnailUrl} alt="" loading="lazy" />
                </Show>
              </div>
              <div class="queue-body">
                <div class="queue-title">{item.media.title || item.media.sourceUrl}</div>
                <div class="queue-meta muted">
                  <Show when={item.media.durationMs > 0}>{formatTime(item.media.durationMs)} · </Show>
                  <Show when={item.addedBy}>by {item.addedBy} · </Show>
                  <StatusBadge item={item} />
                </div>
                <Show when={item.media.status === "downloading"}>
                  <progress max="1" value={item.media.progress} />
                </Show>
              </div>
              <div class="queue-actions">
                <Show when={voteMode() && me() && !item.current}>
                  <button type="button" class={`link vote ${item.voted ? "voted" : ""}`} onClick={() => props.room.commands.vote(item.id)} title="Vote up">
                    ▲ {item.votes}
                  </button>
                </Show>
                <Show when={canManage()}>
                  <Show when={!item.current}>
                    <button type="button" class="link" onClick={() => props.room.commands.jump(item.id)} title="Play now">
                      play
                    </button>
                    <Show when={!voteMode()}>
                      <button type="button" class="link" onClick={() => moveUp(idx())} disabled={idx() < 2} title="Move up">
                        ↑
                      </button>
                      <button type="button" class="link" onClick={() => moveDown(idx())} disabled={idx() >= items().length - 1} title="Move down">
                        ↓
                      </button>
                    </Show>
                  </Show>
                  <Show when={item.media.status === "failed"}>
                    <button type="button" class="link" onClick={() => props.room.commands.retry(item.id)}>
                      retry
                    </button>
                  </Show>
                </Show>
                <Show when={me()}>
                  <button type="button" class="link" onClick={() => setReporting(item)} title="Report">
                    ⚑
                  </button>
                </Show>
                <Show when={canManage() || (me() && item.addedBy === me() && !item.current)}>
                  <button type="button" class="link danger-text" onClick={() => props.room.commands.remove(item.id)} title="Remove">
                    ✕
                  </button>
                </Show>
              </div>
            </li>
          )}
        </For>
        <For each={pending()}>
          {(p) => (
            <li class="queue-item pending" aria-busy="true">
              <div class="thumb">
                <div class="thumb-empty" />
              </div>
              <div class="queue-body">
                <div class="queue-title">{p.title || p.url}</div>
                <div class="queue-meta muted">
                  <span class="ring small" aria-hidden="true" /> {p.next ? "adding next…" : "adding…"}
                </div>
              </div>
            </li>
          )}
        </For>
      </ul>

      <Show when={played().length > 0}>
        <details class="played">
          <summary>
            <span class="section-title">
              Played <span class="muted">{played().length}</span>
            </span>
            <Show when={canManage()}>
              <button type="button" class="link small" onClick={(e) => (e.preventDefault(), props.room.commands.clearPlayed())}>
                Clear
              </button>
            </Show>
          </summary>
          <ul class="list">
            <For each={played()}>
              {(item) => (
                <li class="queue-item played-item">
                  <div class="thumb">
                    <Show when={item.media.thumbnailUrl} fallback={<div class="thumb-empty" />}>
                      <img src={item.media.thumbnailUrl} alt="" loading="lazy" />
                    </Show>
                  </div>
                  <div class="queue-body">
                    <div class="queue-title">{item.media.title || item.media.sourceUrl}</div>
                    <div class="queue-meta muted">
                      <Show when={item.media.durationMs > 0}>{formatTime(item.media.durationMs)} · </Show>
                      <Show when={item.addedBy}>by {item.addedBy} · </Show>
                      finished {new Date(item.playedMs ?? 0).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}
                    </div>
                  </div>
                  <div class="queue-actions">
                    <Show when={canAdd()}>
                      <button type="button" class="link" onClick={() => props.room.commands.replay(item.id)} title="Queue again">
                        play again
                      </button>
                    </Show>
                  </div>
                </li>
              )}
            </For>
          </ul>
        </details>
      </Show>
      <Show when={reporting()}>
        {(item) => (
          <ReportDialog
            mediaId={item().media.id}
            title={item().media.title || item().media.sourceUrl}
            roomSlug={props.room.state.snapshot?.room.slug ?? ""}
            onClose={() => setReporting(null)}
          />
        )}
      </Show>
    </section>
  );
};

const StatusBadge: Component<{ item: QueueEntry }> = (props) => (
  <span class={`badge status-${props.item.media.status}`}>
    {props.item.current ? "now playing" : props.item.media.status}
  </span>
);

export default Queue;
