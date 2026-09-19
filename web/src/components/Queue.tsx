import { createSignal, For, Show, type Component } from "solid-js";

import ReportDialog from "~/components/ReportDialog";
import { formatTime } from "~/lib/format";
import type { RoomStore } from "~/store/room";
import type { QueueEntry } from "~/protocol";

type Props = { room: RoomStore };

const Queue: Component<Props> = (props) => {
  const items = () => props.room.state.snapshot?.queue ?? [];
  const canManage = () => props.room.isModerator();
  const me = () => props.room.state.me;
  const voteMode = () => props.room.state.snapshot?.room.settings.voteMode ?? false;
  const [reporting, setReporting] = createSignal<QueueEntry | null>(null);

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
    <section class="card queue">
      <h2>Queue ({items().length})</h2>
      <Show when={items().length === 0}>
        <p class="muted">Nothing queued.</p>
      </Show>
      <ul class="list">
        <For each={items()}>
          {(item, idx) => (
            <li class={`queue-item ${item.current ? "current" : ""}`}>
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
      </ul>
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
