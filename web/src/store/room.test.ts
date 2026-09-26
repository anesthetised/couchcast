import { createRoot } from "solid-js";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { ChatMessage, QueueEntry, ServerMessage } from "~/protocol";
import { createRoomStore, type RoomStore } from "~/store/room";
import { FakeWebSocket } from "~/test/fakeWebSocket";
import { item, line, snapshot } from "~/test/fixtures";

let dispose = () => {};
let room: RoomStore;
const ws = () => FakeWebSocket.all.at(-1)!;
const deliver = (m: ServerMessage) => ws().deliver(m);
// Commands carry refs; most assertions compare the rest.
const raw = () => ws().sent.map((s) => JSON.parse(s) as { type: string; ref?: number });
const sent = () => raw().map(({ ref: _ref, ...m }) => m);

function open(queue: QueueEntry[] = [], messages: ChatMessage[] = []) {
  createRoot((d) => {
    dispose = d;
    room = createRoomStore("movie-night");
  });
  ws().accept();
  deliver({ type: "welcome", me: "alice", role: "owner", snapshot: snapshot(queue), messages });
}

describe("room store", () => {
  beforeEach(() => {
    FakeWebSocket.all = [];
    vi.stubGlobal("WebSocket", FakeWebSocket);
    vi.useFakeTimers();
  });
  afterEach(() => {
    dispose();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("takes the welcome as the starting state", () => {
    open([item("a", { current: true })], [line(1, "hi")]);
    expect(room.state.me).toBe("alice");
    expect(room.isModerator()).toBe(true);
    expect(room.current()!.id).toBe("a");
    expect(room.state.messages.map((m) => m.body)).toEqual(["hi"]);
    expect(room.status()).toBe("open");
  });

  it("keeps the last 200 chat lines and counts unread ones while hidden", () => {
    open();
    const vis = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    for (let i = 1; i <= 205; i++) deliver(line(i, `m${i}`));
    expect(room.state.messages).toHaveLength(200);
    expect(room.state.messages[0]!.body).toBe("m6");
    expect(room.unread()).toBe(205);
    room.clearUnread();
    expect(room.unread()).toBe(0);
    vis.mockRestore();
  });

  it("applies edits to the line and to quotes of it, and drops deleted lines", () => {
    open([], [line(1, "helo"), line(2, "what?", { replyTo: { id: 1, username: "bob", body: "helo" } })]);
    deliver({ type: "chat.edited", message: { id: 1, body: "hello", createdMs: 1, editedMs: 5 } });
    expect(room.state.messages[0]).toMatchObject({ body: "hello", editedMs: 5 });
    expect(room.state.messages[1]!.replyTo!.body).toBe("hello");
    deliver({ type: "chat.deleted", id: 1 });
    expect(room.state.messages.map((m) => m.id)).toEqual([2]);
    deliver({ type: "chat.cleared" });
    expect(room.state.messages).toEqual([]);
  });

  it("follows the pin", () => {
    open();
    deliver({ type: "chat.pinned", message: line(3, "rules") });
    expect(room.state.snapshot!.room.pinned!.body).toBe("rules");
    deliver({ type: "chat.pinned", message: null });
    expect(room.state.snapshot!.room.pinned).toBeUndefined();
  });

  it("mirrors playback into the snapshot", () => {
    open([item("a", { current: true })]);
    deliver({ type: "playback", itemId: "a", playing: true, positionMs: 1_000, atServerMs: 5, rate: 1.5, seq: 2 });
    expect(room.state.playback).toMatchObject({ playing: true, rate: 1.5 });
    expect(room.state.snapshot!.playback.seq).toBe(2);
  });

  it("shows a pending add until our own item appears", () => {
    open([item("a")]);
    room.commands.add("https://youtu.be/new", { title: "New" });
    expect(sent().at(-1)).toEqual({ type: "queue.add", url: "https://youtu.be/new" });
    expect(room.pending().map((p) => p.title)).toEqual(["New"]);
    // Someone else's item does not settle it.
    deliver({ ...snapshot([item("a"), item("b", { addedBy: "bob" })]) });
    expect(room.pending()).toHaveLength(1);
    deliver({ ...snapshot([item("a"), item("b", { addedBy: "bob" }), item("c")]) });
    expect(room.pending()).toHaveLength(0);
  });

  it("forgets a pending add after a while", () => {
    open();
    room.commands.add("https://youtu.be/slow");
    vi.advanceTimersByTime(15_000);
    expect(room.pending()).toHaveLength(0);
  });

  it("turns a duplicate error into a question instead of a toast", () => {
    open();
    room.commands.add("https://youtu.be/dup", { next: true, title: "Dup" });
    const ref = raw().at(-1)!.ref;
    expect(ref).toBeGreaterThan(0);
    deliver({ type: "error", code: "duplicate", message: "already queued", ref });
    expect(room.pending()).toHaveLength(0);
    expect(room.duplicate()).toEqual({ url: "https://youtu.be/dup", title: "Dup", next: true, message: "already queued" });
    room.commands.add("https://youtu.be/dup", { force: true });
    expect(room.duplicate()).toBeNull();
    expect(sent().at(-1)).toEqual({ type: "queue.add", url: "https://youtu.be/dup", force: true });
  });

  it("settles only the command an error names", async () => {
    const toast = await import("~/lib/toast");
    open();
    room.commands.add("https://youtu.be/a", { title: "A" });
    const addRef = raw().at(-1)!.ref!;
    room.commands.chat("too fast");
    const chatRef = raw().at(-1)!.ref!;
    expect(chatRef).not.toBe(addRef);

    // A chat error leaves the pending add alone and shows as a toast.
    deliver({ type: "error", code: "rate_limited", message: "slow mode: wait 3 s", ref: chatRef });
    expect(room.pending().map((p) => p.title)).toEqual(["A"]);
    expect(toast.toasts().at(-1)?.text).toBe("slow mode: wait 3 s");

    // The add's own error settles it.
    deliver({ type: "error", code: "invalid", message: "this link is not supported", ref: addRef });
    expect(room.pending()).toEqual([]);
  });

  it("expires typing hints, ignoring our own", () => {
    open();
    deliver({ type: "typing", username: "bob" });
    deliver({ type: "typing", username: "alice" });
    deliver({ type: "typing", username: "bob" });
    expect(room.typing()).toEqual(["bob"]);
    vi.advanceTimersByTime(4_000);
    expect(room.typing()).toEqual([]);
  });

  it("floats reactions briefly", () => {
    open();
    deliver({ type: "reaction", username: "bob", emoji: "🔥" });
    expect(room.reactions().map((r) => r.emoji)).toEqual(["🔥"]);
    vi.advanceTimersByTime(2_500);
    expect(room.reactions()).toEqual([]);
  });

  it("ends the session on a kick, and as gone when the room was deleted", () => {
    open();
    deliver({ type: "kicked", reason: "banned by alice" });
    expect(room.ended()).toEqual({ kind: "kicked", reason: "banned by alice" });
    dispose();
    open();
    deliver({ type: "kicked", reason: "room deleted" });
    expect(room.ended()).toEqual({ kind: "gone" });
  });

  it("stops retrying once a REST check says the room is gone", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "room not found" }), { status: 404 }));
    vi.stubGlobal("fetch", fetchMock);
    open();
    ws().drop(1006, "");
    await vi.advanceTimersByTimeAsync(500);
    expect(FakeWebSocket.all).toHaveLength(2);
    ws().drop(1006, "");
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/rooms/movie-night", expect.anything());
    expect(room.ended()).toEqual({ kind: "gone" });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(FakeWebSocket.all).toHaveLength(2);
  });

  it("sends commands in the protocol's shape", () => {
    open();
    room.commands.play(true);
    room.commands.play();
    room.commands.seek(1234.6);
    room.commands.settings({ fairQueue: true });
    room.commands.chat("hi", 7);
    room.commands.chatEdit(7, "hello");
    room.commands.report("buffering", 99.4);
    expect(sent().filter((m) => m.type !== "ping")).toEqual([
      { type: "play", countdown: true },
      { type: "play" },
      { type: "seek", positionMs: 1235 },
      { type: "settings.set", fairQueue: true },
      { type: "chat.send", body: "hi", replyTo: 7 },
      { type: "chat.edit", id: 7, body: "hello" },
      { type: "report", state: "buffering", positionMs: 99 },
    ]);
  });
});
