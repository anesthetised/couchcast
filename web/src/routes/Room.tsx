import { useLocation, useParams } from "@solidjs/router";
import { createResource, createSignal, For, Show, type Component } from "solid-js";

import AddToQueue from "~/components/AddToQueue";
import Chat from "~/components/Chat";
import Members from "~/components/Members";
import Player from "~/components/Player";
import Queue from "~/components/Queue";
import RoomOptions from "~/components/RoomOptions";
import VotePanel from "~/components/VotePanel";
import { ApiError } from "~/lib/api";
import { createFullscreen, readFullscreenPanel, storeFullscreenPanel, type FullscreenPanel } from "~/lib/fullscreen";
import { rooms } from "~/lib/rooms";
import { isModerator } from "~/lib/types";
import { createRoomStore } from "~/store/room";

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
    <Show when={!room.error} fallback={<section class="card error">{errorMessage()}</section>}>
      <Show when={room()} fallback={<p class="muted">Loading…</p>}>
        {(r) => <LiveRoom slug={r().slug} name={r().name} canSettings={isModerator(r().myRole)} />}
      </Show>
    </Show>
  );
};

const LiveRoom: Component<{ slug: string; name: string; canSettings: boolean }> = (props) => {
  const store = createRoomStore(props.slug);
  // Warnings handed over by the create page (unknown invitees, a first
  // video that could not be queued); shown once, dismissable.
  const location = useLocation<{ warnings?: string[] }>();
  const [notices, setNotices] = createSignal<string[]>(location.state?.warnings ?? []);
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

  return (
    <div class="room-layout">
      <div class="room-main">
        <header class="room-header">
          <div>
            <h1>{store.state.snapshot?.room.name ?? props.name}</h1>
            <p class="muted small">
              /r/{props.slug}
              <Show when={store.status() !== "open"}> · {store.status()}</Show>
            </p>
          </div>
          <Show when={props.canSettings}>
            <a class="button" href={`/r/${props.slug}/settings`}>
              Settings
            </a>
          </Show>
        </header>
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
        <Show when={store.lastError()}>{(e) => <p class="card error">{e()}</p>}</Show>
        <div
          class={`stage ${fs.active() ? "fullscreen" : ""} ${fs.idle() ? "idle" : ""}`}
          ref={stage}
          onMouseMove={fs.touch}
          onClick={fs.touch}
          onKeyDown={fs.touch}
        >
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
        <VotePanel room={store} />
        <AddToQueue room={store} />
      </div>
      <aside class="room-side">
        <Queue room={store} />
        <Chat room={store} />
        <Members room={store} />
        <RoomOptions room={store} />
      </aside>
    </div>
  );
};

export default Room;
