import { createResource, createSignal, For, onMount, Show, type Component } from "solid-js";

import { api } from "~/lib/api";
import { trapFocus } from "~/lib/focusTrap";
import { formatDuration, formatTime } from "~/lib/format";

type Entry = { url: string; title: string; durationMs: number; thumbnailUrl?: string };
type Playlist = { title: string; total: number; entries: Entry[] };

type Props = {
  url: string;
  canPlayNext: boolean;
  onAdd: (urls: string[], next: boolean) => void;
  onClose: () => void;
};

// PlaylistPicker lists the first entries of a playlist and queues the
// ones the viewer keeps ticked, in playlist order.
const PlaylistPicker: Component<Props> = (props) => {
  let dialog!: HTMLDivElement;
  const [list] = createResource(() => props.url, (u) => api<Playlist>(`/api/v1/media/playlist?url=${encodeURIComponent(u)}`));
  const [off, setOff] = createSignal<Set<string>>(new Set());

  onMount(() => trapFocus(dialog, props.onClose));

  const entries = () => list()?.entries ?? [];
  const picked = () => entries().filter((e) => !off().has(e.url));
  const toggle = (url: string) => {
    const next = new Set(off());
    if (next.has(url)) next.delete(url);
    else next.add(url);
    setOff(next);
  };
  const allOn = () => off().size === 0;
  const toggleAll = () => setOff(allOn() ? new Set(entries().map((e) => e.url)) : new Set<string>());
  const totalMs = () => picked().reduce((sum, e) => sum + e.durationMs, 0);

  const add = (next: boolean) => {
    const urls = picked().map((e) => e.url);
    if (!urls.length) return;
    props.onAdd(urls, next);
    props.onClose();
  };

  return (
    <div class="modal-backdrop" onClick={props.onClose}>
      <div class="card modal playlist-picker" role="dialog" aria-modal="true" aria-labelledby="playlist-title" ref={dialog} onClick={(e) => e.stopPropagation()}>
        <Show when={!list.error} fallback={<p class="error">{list.error instanceof Error ? list.error.message : "Could not read this playlist."}</p>}>
          <Show when={list()} fallback={<p class="muted"><span class="ring small" aria-hidden="true" /> Reading the playlist…</p>}>
            {(pl) => (
              <>
                <header class="playlist-head">
                  <h2 id="playlist-title">{pl().title || "Playlist"}</h2>
                  <span class="muted small">
                    {pl().total > pl().entries.length ? `First ${pl().entries.length} of ${pl().total} videos` : `${pl().entries.length} videos`}
                  </span>
                </header>
                <label class="radio playlist-all">
                  <input type="checkbox" checked={allOn()} onChange={toggleAll} />
                  Select all
                </label>
                <ul class="playlist-list">
                  <For each={pl().entries} fallback={<li class="muted">This playlist has no playable videos.</li>}>
                    {(e, i) => (
                      <li>
                        <label class="playlist-row">
                          <input type="checkbox" checked={!off().has(e.url)} onChange={() => toggle(e.url)} />
                          <span class="muted small playlist-index">{i() + 1}</span>
                          <Show when={e.thumbnailUrl} fallback={<span class="playlist-thumb" />}>
                            {(src) => <img class="playlist-thumb" src={src()} alt="" loading="lazy" />}
                          </Show>
                          <span class="playlist-title">{e.title}</span>
                          <Show when={e.durationMs > 0}>
                            <span class="muted small">{formatTime(e.durationMs)}</span>
                          </Show>
                        </label>
                      </li>
                    )}
                  </For>
                </ul>
              </>
            )}
          </Show>
        </Show>
        <div class="actions playlist-actions">
          <span class="muted small">
            {picked().length} selected
            <Show when={totalMs() > 0}> · {formatDuration(totalMs())}</Show>
          </span>
          <Show when={props.canPlayNext}>
            <button type="button" class="ghost" disabled={!picked().length} onClick={() => add(true)} title="Queue them right after the current video">
              Play next
            </button>
          </Show>
          <button type="button" disabled={!picked().length} onClick={() => add(false)}>
            Add {picked().length || ""}
          </button>
          <button type="button" class="link" onClick={props.onClose}>
            Cancel
          </button>
        </div>
      </div>
    </div>
  );
};

export default PlaylistPicker;
