import { createResource, Show, type Component } from "solid-js";

import { api } from "~/lib/api";
import { formatTime } from "~/lib/format";

type Preview = { title: string; durationMs: number; thumbnailUrl?: string };
type Props = { url: string; canAdd: boolean; onAdd: (url: string) => void };

// One probe per URL per page load; a failure is remembered too so a bad
// link is not retried for every render.
const cache = new Map<string, Promise<Preview | null>>();

function probe(url: string): Promise<Preview | null> {
  let p = cache.get(url);
  if (!p) {
    p = api<Preview>(`/api/v1/media/probe?url=${encodeURIComponent(url)}`).catch(() => null);
    cache.set(url, p);
  }
  return p;
}

// LinkCard unfurls a video link in chat: thumbnail, title, duration and
// a way to queue it. Falls back to the bare link while loading or when
// the probe fails.
const LinkCard: Component<Props> = (props) => {
  const [preview] = createResource(() => props.url, probe);

  return (
    <>
      <a href={props.url} target="_blank" rel="noopener noreferrer">
        {props.url}
      </a>
      <Show when={preview()}>
        {(p) => (
          <span class="link-card">
            <Show when={p().thumbnailUrl}>{(src) => <img src={src()} alt="" loading="lazy" />}</Show>
            <span class="link-card-body">
              <span class="link-card-title">{p().title || props.url}</span>
              <span class="muted small">
                <Show when={p().durationMs > 0}>{formatTime(p().durationMs)}</Show>
                <Show when={props.canAdd}>
                  {" "}
                  <button type="button" class="link chat-queue" title="Add to queue" onClick={() => props.onAdd(props.url)}>
                    + queue
                  </button>
                </Show>
              </span>
            </span>
          </span>
        )}
      </Show>
    </>
  );
};

export default LinkCard;
