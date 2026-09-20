import { createSignal, onCleanup } from "solid-js";
import { createStore, reconcile } from "solid-js/store";

import { ApiError } from "~/lib/api";
import { ClockSync } from "~/lib/clock";
import { rooms } from "~/lib/rooms";
import { toast } from "~/lib/toast";
import type { RoomRole } from "~/lib/types";
import { RoomSocket, type SocketStatus } from "~/lib/ws";
import type { ChatMessage, ClientMessage, Playback, Snapshot } from "~/protocol";

export type RoomEnd = { kind: "gone" } | { kind: "kicked"; reason: string };
export type PendingAdd = { id: number; url: string; title?: string; next: boolean };

const PENDING_TTL_MS = 15_000;

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
  // ended is set when the room is over for this client: kicked, banned or
  // the room no longer exists. The socket stops reconnecting.
  const [ended, setEnded] = createSignal<RoomEnd | null>(null);
  const [attempts, setAttempts] = createSignal(0);
  // unread counts live chat lines that arrived while the tab was hidden;
  // the page resets it when the tab is visible again.
  const [unread, setUnread] = createSignal(0);
  // pending holds links sent with queue.add until the snapshot lists them
  // (or the server rejects them), so the queue can show them right away.
  const [pending, setPending] = createSignal<PendingAdd[]>([]);
  let pendingSeq = 0;
  let knownItems = new Set<string>();
  const dropPending = (id: number) => setPending((p) => p.filter((x) => x.id !== id));
  const [clockInfo, setClockInfo] = createSignal({ offset: 0, rtt: 0 });

  clock.onUpdate = () => setClockInfo({ offset: clock.offset, rtt: clock.rtt });
  socket.onStatus = (s) => {
    setStatus(s);
    setAttempts(socket.attempts);
    if (s === "open") clock.start();
    else clock.stop();
  };
  // A drop with a server-side reason (room deleted) or a room that has
  // gone missing or closed to us since (404/403 on a quick REST check)
  // ends the session instead of retrying forever.
  socket.onDrop = async (reason, n) => {
    setAttempts(n);
    if (reason === "room deleted") {
      setEnded({ kind: "gone" });
      return false;
    }
    if (n < 2) return true;
    try {
      await rooms.get(slug);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setEnded({ kind: "gone" });
        return false;
      }
      if (err instanceof ApiError && err.status === 403) {
        setEnded({ kind: "kicked", reason: err.message });
        return false;
      }
    }
    return true;
  };

  socket.subscribe((msg) => {
    switch (msg.type) {
      case "welcome":
        knownItems = new Set(msg.snapshot.queue.map((q) => q.id));
        setPending([]);
        setState("me", msg.me);
        setState("role", msg.role);
        setState("snapshot", reconcile(msg.snapshot));
        setState("playback", msg.snapshot.playback);
        setState("messages", msg.messages);
        break;
      case "room.state":
        setState("snapshot", reconcile(msg));
        setState("playback", msg.playback);
        if (pending().length) {
          // The media's stored URL may differ from what was pasted, so a
          // pending add is settled by our own new items appearing.
          const mine = msg.queue.filter((q) => q.addedBy === state.me && !knownItems.has(q.id)).length;
          if (mine > 0) setPending((p) => p.slice(mine));
        }
        knownItems = new Set(msg.queue.map((q) => q.id));
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
        if (document.visibilityState === "hidden") setUnread((n) => n + 1);
        break;
      case "chat.deleted":
        setState("messages", (m) => m.filter((x) => x.id !== msg.id));
        break;
      case "error":
        toast(msg.message, "error");
        // Most errors here answer a command; a pending add is the likeliest.
        setPending((p) => p.slice(0, -1));
        break;
      case "kicked":
        setEnded(msg.reason === "room deleted" ? { kind: "gone" } : { kind: "kicked", reason: msg.reason });
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
    attempts,
    ended,
    unread,
    clearUnread: () => setUnread(0),
    pending,
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
      rate: (rate: number) => send({ type: "rate.set", rate }),
      next: () => send({ type: "next" }),
      jump: (itemId: string) => send({ type: "jump", itemId }),
      add: (url: string, opts: { next?: boolean; title?: string } = {}) => {
        const id = ++pendingSeq;
        setPending((p) => [...p, { id, url, title: opts.title, next: opts.next ?? false }]);
        window.setTimeout(() => dropPending(id), PENDING_TTL_MS);
        return send({ type: "queue.add", url, next: opts.next || undefined });
      },
      replay: (itemId: string) => send({ type: "queue.replay", itemId }),
      clearPlayed: () => send({ type: "queue.clearPlayed" }),
      remove: (itemId: string) => send({ type: "queue.remove", itemId }),
      move: (itemId: string, afterId: string | null) => send({ type: "queue.move", itemId, afterId }),
      retry: (itemId: string) => send({ type: "queue.retry", itemId }),
      vote: (itemId: string) => send({ type: "queue.vote", itemId }),
      skipVote: () => send({ type: "skip.vote" }),
      endSession: () => send({ type: "session.end" }),
      settings: (patch: { voteMode?: boolean; skipThreshold?: number; viewersCanAdd?: boolean; loop?: boolean }) =>
        send({ type: "settings.set", ...patch }),
      chat: (body: string) => send({ type: "chat.send", body }),
      chatDelete: (id: number) => send({ type: "chat.delete", id }),
      report: (s: "playing" | "buffering" | "ended", positionMs: number) =>
        send({ type: "report", state: s, positionMs: Math.round(positionMs) }),
    },
  };
}

export type RoomStore = ReturnType<typeof createRoomStore>;
