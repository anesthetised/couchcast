import { createSignal, onCleanup } from "solid-js";
import { createStore, reconcile } from "solid-js/store";

import { ClockSync } from "~/lib/clock";
import type { RoomRole } from "~/lib/types";
import { RoomSocket, type SocketStatus } from "~/lib/ws";
import type { ChatMessage, ClientMessage, Playback, Snapshot } from "~/protocol";

export interface RoomState {
  snapshot: Snapshot | null;
  playback: Playback | null;
  messages: ChatMessage[];
  me: string | null;
  role?: RoomRole;
}

// createRoomStore owns the socket for one room and exposes reactive
// state plus command helpers. Call inside a component: it cleans up when
// the component is disposed.
export function createRoomStore(slug: string) {
  const socket = new RoomSocket(slug);
  const clock = new ClockSync(socket);

  const [state, setState] = createStore<RoomState>({ snapshot: null, playback: null, messages: [], me: null });
  const [status, setStatus] = createSignal<SocketStatus>("connecting");
  const [lastError, setLastError] = createSignal<string | null>(null);
  const [clockInfo, setClockInfo] = createSignal({ offset: 0, rtt: 0 });

  clock.onUpdate = () => setClockInfo({ offset: clock.offset, rtt: clock.rtt });
  socket.onStatus = (s) => {
    setStatus(s);
    if (s === "open") clock.start();
    else clock.stop();
  };

  socket.subscribe((msg) => {
    switch (msg.type) {
      case "welcome":
        setState("me", msg.me);
        setState("role", msg.role);
        setState("snapshot", reconcile(msg.snapshot));
        setState("playback", msg.snapshot.playback);
        setState("messages", msg.messages);
        break;
      case "room.state":
        setState("snapshot", reconcile(msg));
        setState("playback", msg.playback);
        break;
      case "playback":
        setState("playback", { ...msg });
        if (state.snapshot) setState("snapshot", "playback", { ...msg });
        break;
      case "pong":
        clock.handlePong(msg.t0, msg.t1);
        break;
      case "chat.message":
        setState("messages", (m) => [...m.slice(-199), msg]);
        break;
      case "chat.deleted":
        setState("messages", (m) => m.filter((x) => x.id !== msg.id));
        break;
      case "error":
        setLastError(msg.message);
        window.setTimeout(() => setLastError(null), 4000);
        break;
      case "kicked":
        setLastError(`Disconnected: ${msg.reason}`);
        break;
    }
  });

  socket.connect();
  onCleanup(() => {
    clock.stop();
    socket.close();
  });

  const send = (msg: ClientMessage) => socket.send(msg);

  return {
    state,
    status,
    lastError,
    clock,
    clockInfo,
    send,
    // Convenience accessors.
    current: () => state.snapshot?.queue.find((q) => q.current) ?? null,
    isModerator: () => state.role === "owner" || state.role === "moderator",
    commands: {
      play: () => send({ type: "play" }),
      pause: () => send({ type: "pause" }),
      seek: (positionMs: number) => send({ type: "seek", positionMs: Math.round(positionMs) }),
      next: () => send({ type: "next" }),
      jump: (itemId: string) => send({ type: "jump", itemId }),
      add: (url: string) => send({ type: "queue.add", url }),
      remove: (itemId: string) => send({ type: "queue.remove", itemId }),
      move: (itemId: string, afterId: string | null) => send({ type: "queue.move", itemId, afterId }),
      retry: (itemId: string) => send({ type: "queue.retry", itemId }),
      vote: (itemId: string) => send({ type: "queue.vote", itemId }),
      skipVote: () => send({ type: "skip.vote" }),
      settings: (patch: { voteMode?: boolean; skipThreshold?: number; viewersCanAdd?: boolean }) =>
        send({ type: "settings.set", ...patch }),
      chat: (body: string) => send({ type: "chat.send", body }),
      chatDelete: (id: number) => send({ type: "chat.delete", id }),
      report: (s: "playing" | "buffering" | "ended", positionMs: number) =>
        send({ type: "report", state: s, positionMs: Math.round(positionMs) }),
    },
  };
}

export type RoomStore = ReturnType<typeof createRoomStore>;
