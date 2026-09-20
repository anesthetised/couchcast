import { createSignal, Match, onCleanup, Show, Switch, type Component } from "solid-js";

import { api, ApiError } from "~/lib/api";
import { formatTime } from "~/lib/format";
import type { RoomStore } from "~/store/room";

type Props = { room: RoomStore };

type Preview = { title: string; durationMs: number; thumbnailUrl?: string; status?: string };
type Lookup = { url: string; state: "loading" } | { url: string; state: "ok"; preview: Preview } | { url: string; state: "error"; message: string };

const PROBE_DEBOUNCE_MS = 400;

const previewOf = (l: Lookup) => (l.state === "ok" ? l.preview : null);
const errorOf = (l: Lookup) => (l.state === "error" ? l.message : null);

// AddToQueue takes a link, shows what it resolves to and queues it. The
// preview is a courtesy: Add still works while a probe is in flight, and
// a probe failure only blocks links the server says it cannot read.
const AddToQueue: Component<Props> = (props) => {
  const [url, setUrl] = createSignal("");
  const [lookup, setLookup] = createSignal<Lookup | null>(null);
  let timer: number | null = null;
  let seq = 0;

  const canAdd = () =>
    props.room.isModerator() ||
    (props.room.state.me !== null && (props.room.state.snapshot?.room.settings.viewersCanAdd ?? false));

  const probe = (raw: string) => {
    if (timer !== null) window.clearTimeout(timer);
    const u = raw.trim();
    if (!/^https?:\/\/\S+$/.test(u)) {
      setLookup(null);
      return;
    }
    const mine = ++seq;
    setLookup({ url: u, state: "loading" });
    timer = window.setTimeout(async () => {
      try {
        const preview = await api<Preview>(`/api/v1/media/probe?url=${encodeURIComponent(u)}`);
        if (mine === seq) setLookup({ url: u, state: "ok", preview });
      } catch (err) {
        if (mine !== seq) return;
        // Throttled or timed out: let the user try Add anyway.
        if (err instanceof ApiError && (err.status === 429 || err.status === 504)) setLookup(null);
        else setLookup({ url: u, state: "error", message: err instanceof Error ? err.message : String(err) });
      }
    }, PROBE_DEBOUNCE_MS);
  };
  onCleanup(() => {
    if (timer !== null) window.clearTimeout(timer);
  });

  const blocked = () => lookup()?.state === "error";
  // "Play next" only makes sense when a moderator orders the queue by hand
  // and something is playing.
  const canPlayNext = () =>
    props.room.isModerator() && !(props.room.state.snapshot?.room.settings.voteMode ?? false) && props.room.current() !== null;

  const add = (next: boolean) => {
    const u = url().trim();
    if (!u || blocked()) return;
    const l = lookup();
    props.room.commands.add(u, { next, title: l?.state === "ok" ? l.preview.title : undefined });
    setUrl("");
    setLookup(null);
    seq++;
  };

  const submit = (e: SubmitEvent) => {
    e.preventDefault();
    add(false);
  };

  return (
    <Show when={canAdd()} fallback={<p class="muted small">{props.room.state.me ? "Only moderators can add videos here." : "Log in to add videos."}</p>}>
      <form class="add-form" onSubmit={submit}>
        <div class="add-field">
          <input
            type="url"
            placeholder="Paste a YouTube or video link"
            required
            value={url()}
            onInput={(e) => {
              setUrl(e.currentTarget.value);
              probe(e.currentTarget.value);
            }}
            autofocus={(props.room.state.snapshot?.queue.length ?? 1) === 0}
          />
          <Show when={lookup()}>
            {(l) => (
              <div class="add-preview" classList={{ error: l().state === "error" }}>
                <Switch>
                  <Match when={l().state === "loading"}>
                    <span class="ring small" aria-hidden="true" />
                    <span class="muted">Looking up…</span>
                  </Match>
                  <Match when={errorOf(l())}>{(m) => <span>{m()}</span>}</Match>
                  <Match when={previewOf(l())}>
                    {(p) => (
                      <>
                        <Show when={p().thumbnailUrl}>{(src) => <img src={src()} alt="" />}</Show>
                        <span class="add-preview-body">
                          <strong>{p().title || l().url}</strong>
                          <span class="muted small">
                            <Show when={p().durationMs > 0}>{formatTime(p().durationMs)}</Show>
                            <Show when={p().status === "ready"}> · already prepared</Show>
                            <Show when={p().status && p().status !== "ready" && p().status !== "failed"}> · being prepared</Show>
                          </span>
                        </span>
                      </>
                    )}
                  </Match>
                </Switch>
              </div>
            )}
          </Show>
        </div>
        <div class="actions add-actions">
          <button type="submit" disabled={blocked()}>
            Add
          </button>
          <Show when={canPlayNext()}>
            <button type="button" class="ghost" disabled={blocked() || !url().trim()} onClick={() => add(true)} title="Queue right after the current video">
              Play next
            </button>
          </Show>
        </div>
      </form>
    </Show>
  );
};

export default AddToQueue;
