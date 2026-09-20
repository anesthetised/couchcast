import { useLocation, useParams } from "@solidjs/router";
import { createResource, createSignal, For, Show, type Component } from "solid-js";

import AddToQueue from "~/components/AddToQueue";
import Chat from "~/components/Chat";
import Player from "~/components/Player";
import Queue from "~/components/Queue";
import { ApiError } from "~/lib/api";
import { createFullscreen, readFullscreenPanel, storeFullscreenPanel, type FullscreenPanel } from "~/lib/fullscreen";
import { rooms } from "~/lib/rooms";
import { toast } from "~/lib/toast";
import { isModerator } from "~/lib/types";
import { createRoomStore, type RoomEnd, type RoomStore } from "~/store/room";

// Room page: the REST fetch establishes access (401/403 → message), then
// the WebSocket store drives everything live.
const Room: Component = () => {
  const params = useParams<{ slug: string }>();
  const [room] = createResource(() => params.slug, rooms.get);

  const errorMessage = () => {
    const err = room.error as unknown;
    if (err instanceof ApiError) return err.status === 401 ? "Log in to view this room." : err.message;
    return err ? String(err) : null;
  };

  return (
    <Show when={!room.error} fallback={<div class="empty">{errorMessage()}</div>}>
      <Show when={room()} fallback={<p class="muted">Loading…</p>}>
        {(r) => <LiveRoom slug={r().slug} name={r().name} visibility={r().visibility} canSettings={isModerator(r().myRole)} />}
      </Show>
    </Show>
  );
};

const LiveRoom: Component<{ slug: string; name: string; visibility: string; canSettings: boolean }> = (props) => {
  const store = createRoomStore(props.slug);
  let stage: HTMLDivElement | undefined;
  const fs = createFullscreen(() => stage);
  const usePanel = (panel: FullscreenPanel) => {
    const [on, setOn] = createSignal(readFullscreenPanel(panel));
    const toggle = () => {
      const next = !on();
      setOn(next);
      storeFullscreenPanel(panel, next);
    };
    return { on, toggle };
  };
  const chatPanel = usePanel("chat");
  const queuePanel = usePanel("queue");

  // Warnings handed over by the create page; shown once, dismissable.
  const location = useLocation<{ warnings?: string[] }>();
  const [notices, setNotices] = createSignal<string[]>(location.state?.warnings ?? []);

  const snap = () => store.state.snapshot;
  const live = () => Boolean(store.state.playback?.playing && store.current()?.media.status === "ready");
  const viewers = () => (snap()?.members.length ?? 0) + (snap()?.guests ?? 0);

  return (
    <div class="room">
      <div class="room-main">
        <RoomHeader store={store} slug={props.slug} name={snap()?.room.name ?? props.name} visibility={snap()?.room.visibility ?? props.visibility} live={live()} viewers={viewers()} canSettings={props.canSettings} />

        <Show when={notices().length > 0}>
          <div class="banner">
            <ul class="list">
              <For each={notices()}>{(n) => <li class="row">{n}</li>}</For>
            </ul>
            <button type="button" class="link" onClick={() => setNotices([])}>
              Dismiss
            </button>
          </div>
        </Show>
        <Show when={store.lastError()}>{(e) => <p class="notice">{e()}</p>}</Show>

        <div
          class={`stage ${fs.active() ? "fullscreen" : ""} ${fs.idle() ? "idle" : ""}`}
          ref={stage}
          onMouseMove={fs.touch}
          onClick={fs.touch}
          onKeyDown={fs.touch}
        >
          <Show when={!store.ended() && store.status() !== "open" && (store.attempts() > 0 || store.status() === "closed")}>
            <div class="reconnecting" role="status">
              <span class="ring small" aria-hidden="true" />
              Reconnecting{store.attempts() > 1 ? ` · attempt ${store.attempts()}` : ""}…
            </div>
          </Show>
          <Show when={store.ended()}>
            {(end) => (
              <div class="video-overlay ended" role="alert">
                <strong>{endTitle(end())}</strong>
                <span class="muted">{endDetail(end())}</span>
                <a class="button ghost" href="/">
                  Back to rooms
                </a>
              </div>
            )}
          </Show>
          <Player
            room={store}
            onFullscreen={fs.toggle}
            isFullscreen={fs.active()}
            chatVisible={chatPanel.on()}
            onToggleChat={chatPanel.toggle}
            queueVisible={queuePanel.on()}
            onToggleQueue={queuePanel.toggle}
          />
          <Show when={fs.active() && queuePanel.on()}>
            <div class="fs-panel fs-queue">
              <Queue room={store} />
            </div>
          </Show>
          <Show when={fs.active() && chatPanel.on()}>
            <div class="fs-panel fs-chat">
              <Chat room={store} />
            </div>
          </Show>
        </div>

        <div class="room-actions">
          <AddToQueue room={store} />
          <SkipVote store={store} />
        </div>

        <section class="up-next">
          <Queue room={store} />
        </section>
      </div>

      <aside class="room-chat">
        <Chat room={store} />
      </aside>
    </div>
  );
};

// RoomHeader: title, badges, viewer count and the moderator menu.
const RoomHeader: Component<{
  store: RoomStore;
  slug: string;
  name: string;
  visibility: string;
  live: boolean;
  viewers: number;
  canSettings: boolean;
}> = (props) => {
  const settings = () => props.store.state.snapshot?.room.settings;

  return (
    <header class="room-head">
      <div class="room-title">
        <h1>{props.name}</h1>
        <div class="room-meta">
          <Show when={props.live}>
            <span class="badge live">live</span>
          </Show>
          <Show when={props.visibility === "private"}>
            <span class="badge private">🔒 private</span>
          </Show>
          <button type="button" class="link small copy" title="Copy link" onClick={() => void copyLink(props.slug)}>
            /r/{props.slug} ⧉
          </button>
          <span class="muted small">·</span>
          <span class="muted small">
            {props.viewers} watching
          </span>
        </div>
      </div>
      <div class="actions">
        <Show when={props.store.isModerator() && settings()}>
          {(s) => (
            <details class="menu" onKeyDown={(e) => e.key === "Escape" && ((e.currentTarget as HTMLDetailsElement).open = false)}>
              <summary class="button ghost">Options</summary>
              <div class="menu-body">
                <label class="radio">
                  <input type="checkbox" checked={s().voteMode} onChange={(e) => props.store.commands.settings({ voteMode: e.currentTarget.checked })} />
                  Vote mode
                </label>
                <label class="radio">
                  Skip at
                  <select value={String(s().skipThreshold)} onChange={(e) => props.store.commands.settings({ skipThreshold: Number(e.currentTarget.value) })}>
                    <option value="0.25">25% of viewers</option>
                    <option value="0.5">50% of viewers</option>
                    <option value="0.75">75% of viewers</option>
                    <option value="1">everyone</option>
                  </select>
                </label>
                <label class="radio">
                  <input type="checkbox" checked={s().viewersCanAdd} onChange={(e) => props.store.commands.settings({ viewersCanAdd: e.currentTarget.checked })} />
                  Viewers can add videos
                </label>
              </div>
            </details>
          )}
        </Show>
        <Show when={props.canSettings}>
          <a class="button ghost" href={`/r/${props.slug}/settings`}>
            Settings
          </a>
        </Show>
      </div>
    </header>
  );
};

function endTitle(end: RoomEnd): string {
  if (end.kind === "gone") return "This room is gone";
  switch (end.reason) {
    case "banned":
      return "You have been banned";
    case "removed from room":
      return "You were removed from this room";
    default:
      return "Disconnected";
  }
}

function endDetail(end: RoomEnd): string {
  if (end.kind === "gone") return "It was deleted by its owner or an administrator.";
  switch (end.reason) {
    case "banned":
    case "removed from room":
      return "A moderator ended your access.";
    default:
      return end.reason;
  }
}

async function copyLink(slug: string) {
  const url = `${location.origin}/r/${slug}`;
  try {
    await navigator.clipboard.writeText(url);
    toast("Link copied");
  } catch {
    toast(url, "info");
  }
}

// SkipVote sits next to the add form while vote mode is on.
const SkipVote: Component<{ store: RoomStore }> = (props) => {
  const snap = () => props.store.state.snapshot;
  const voteMode = () => snap()?.room.settings.voteMode ?? false;

  return (
    <Show when={voteMode() && props.store.current()}>
      <Show when={props.store.state.me} fallback={<span class="muted small">Log in to vote.</span>}>
        <button type="button" class={snap()?.skipVoted ? "ghost" : "ghost"} onClick={() => props.store.commands.skipVote()}>
          {snap()?.skipVoted ? "Cancel skip vote" : "Vote to skip"} · {snap()?.skipVotes ?? 0}/{snap()?.skipNeeded ?? 0}
        </button>
      </Show>
    </Show>
  );
};

export default Room;
