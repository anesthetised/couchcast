import { createSignal, lazy, Match, onCleanup, Show, Switch, type Component } from "solid-js";

import { api, ApiError } from "~/lib/api";
import { toast } from "~/lib/toast";
import { formatTime } from "~/lib/format";
import type { RoomStore } from "~/store/room";

// Opened on demand; loaded the first time.
const PlaylistPicker = lazy(() => import("~/components/PlaylistPicker"));

type Props = { room: RoomStore };

type Preview = { title: string; durationMs: number; thumbnailUrl?: string; status?: string; playlistUrl?: string };
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
  // A playlist link: the form offers the picker instead of queueing the
  // link itself (a bare playlist) or next to it (a video in a playlist).
  const playlistOf = () => {
    const l = lookup();
    return l?.state === "ok" ? (l.preview.playlistUrl ?? null) : null;
  };
  const bareList = () => playlistOf() !== null && !(previewOf(lookup()!)?.title ?? "");
  const [picker, setPicker] = createSignal<string | null>(null);
  const importMany = (urls: string[], next: boolean) => {
    props.room.commands.addMany(urls, next);
    toast(`Adding ${urls.length} ${urls.length === 1 ? "video" : "videos"}…`);
    setUrl("");
    setLookup(null);
    seq++;
  };
  // "Play next" only makes sense when a moderator orders the queue by hand
  // and something is playing.
  const canPlayNext = () =>
    props.room.isModerator() &&
    !(props.room.state.snapshot?.room.settings.voteMode ?? false) &&
    !(props.room.state.snapshot?.room.settings.fairQueue ?? false) &&
    props.room.current() !== null;

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
    const list = playlistOf();
    if (list && bareList()) return setPicker(list);
    add(false);
  };

  // The server refused a duplicate; the form asks once and forces it.
  const dup = () => props.room.duplicate();
  const addAnyway = () => {
    const d = dup();
    if (!d) return;
    props.room.commands.add(d.url, { next: d.next, title: d.title, force: true });
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
          <Show when={dup()}>
            {(d) => (
              <div class="add-preview warn" role="alert">
                <span class="add-preview-body">
                  <strong>{d().title || d().url}</strong>
                  <span>{capitalize(d().message)}.</span>
                </span>
                <button type="button" class="ghost small" onClick={addAnyway}>
                  Add anyway
                </button>
                <button type="button" class="link small" onClick={props.room.dismissDuplicate}>
                  Cancel
                </button>
              </div>
            )}
          </Show>
          <Show when={lookup()}>
            {(l) => (
              <div class="add-preview" classList={{ error: l().state === "error" }}>
                <Switch>
                  <Match when={l().state === "loading"}>
                    <span class="ring small" aria-hidden="true" />
                    <span class="muted">Looking up…</span>
                  </Match>
                  <Match when={errorOf(l())}>{(m) => <span>{m()}</span>}</Match>
                  <Match when={bareList()}>
                    <span class="add-preview-body">
                      <strong>Playlist</strong>
                      <span class="muted small">Pick the videos to queue</span>
                    </span>
                    <button type="button" class="ghost small" onClick={() => setPicker(playlistOf())}>
                      Choose videos…
                    </button>
                  </Match>
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
                        <Show when={p().playlistUrl}>
                          {(list) => (
                            <button type="button" class="link small add-playlist" onClick={() => setPicker(list())}>
                              Whole playlist…
                            </button>
                          )}
                        </Show>
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
      <Show when={picker()}>
        {(list) => <PlaylistPicker url={list()} canPlayNext={canPlayNext()} onAdd={importMany} onClose={() => setPicker(null)} />}
      </Show>
    </Show>
  );
};

const capitalize = (s: string) => (s ? s[0]!.toUpperCase() + s.slice(1) : s);

export default AddToQueue;
