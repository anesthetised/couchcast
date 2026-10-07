import { fireEvent, render, screen } from "@solidjs/testing-library";
import { createSignal } from "solid-js";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import ChatComposer from "~/components/ChatComposer";
import type { ChatMessage } from "~/protocol";
import { createRoomStore, type RoomStore } from "~/store/room";
import { FakeWebSocket } from "~/test/fakeWebSocket";
import { line, snapshot } from "~/test/fixtures";

// The composer on its own: the room store for commands and names, and the
// reply/edit state Chat would hold.
let room: RoomStore;
const ws = () => FakeWebSocket.all.at(-1)!;
const sent = () =>
  ws()
    .sent.map((s) => JSON.parse(s) as Record<string, unknown>)
    .filter((m) => m.type !== "ping")
    .map(({ ref: _ref, ...m }) => m);

function mount(messages: ChatMessage[] = [], editLast = () => false) {
  const [replyTo, setReplyTo] = createSignal<ChatMessage | null>(null);
  const [editing, setEditing] = createSignal<ChatMessage | null>(null);
  const onSent = vi.fn();
  render(() => {
    room = createRoomStore("movie-night");
    return (
      <ChatComposer
        room={room}
        replyTo={replyTo()}
        editing={editing()}
        onCancelReply={() => setReplyTo(null)}
        onCancelEdit={() => setEditing(null)}
        onEditLast={editLast}
        onSent={onSent}
      />
    );
  });
  ws().accept();
  const snap = snapshot();
  snap.members = [{ username: "bob", role: "member", buffering: false }, { username: "bea", role: "member", buffering: false }];
  ws().deliver({ type: "welcome", me: "alice", role: "member", snapshot: snap, messages });
  const input = screen.getByPlaceholderText("Say something") as HTMLInputElement;
  const type = (value: string) => {
    input.setSelectionRange(value.length, value.length);
    fireEvent.input(input, { target: { value } });
  };
  return { input, type, setReplyTo, setEditing, editing, onSent };
}

describe("ChatComposer", () => {
  beforeEach(() => {
    FakeWebSocket.all = [];
    vi.stubGlobal("WebSocket", FakeWebSocket);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  it("completes @names from members and speakers with the keyboard", async () => {
    const { input, type } = mount([line(1, "hi", { username: "bert" })]);
    type("hey @b");
    const options = screen.getAllByRole("option").map((o) => o.textContent);
    expect(options).toEqual(["bob", "bea", "bert"]);
    fireEvent.keyDown(input, { key: "ArrowDown" });
    fireEvent.keyDown(input, { key: "Tab" });
    expect(input.value).toBe("hey @bea ");
    expect(screen.queryByRole("option")).toBeNull();
  });

  it("completes :emoji: codes and closes the menu with Escape", () => {
    const { input, type } = mount();
    type("so :smil");
    expect(screen.getAllByRole("option").length).toBeGreaterThan(0);
    fireEvent.keyDown(input, { key: "Escape" });
    expect(screen.queryByRole("option")).toBeNull();
    type("so :smile");
    fireEvent.keyDown(input, { key: "Enter" });
    expect(input.value).toMatch(/^so \p{Extended_Pictographic} $/u);
  });

  it("sends a reply and hands back to Chat", () => {
    const { input, type, setReplyTo, onSent } = mount();
    setReplyTo(line(7, "which one?"));
    expect(screen.getByText("Replying to")).toBeTruthy();
    type("the first");
    fireEvent.submit(input.closest("form")!);
    expect(sent().at(-1)).toEqual({ type: "chat.send", body: "the first", replyTo: 7 });
    expect(input.value).toBe("");
    expect(onSent).toHaveBeenCalledOnce();
  });

  it("edits a line: loads its text, saves only a change, Escape cancels", () => {
    const { input, type, setEditing, editing } = mount();
    setEditing(line(3, "see you at 9", { username: "alice" }));
    expect(input.value).toBe("see you at 9");
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
    fireEvent.submit(input.closest("form")!);
    expect(sent().filter((m) => m.type === "chat.edit")).toEqual([]); // unchanged
    expect(editing()).toBeNull();
    expect(input.value).toBe("");

    setEditing(line(3, "see you at 9", { username: "alice" }));
    type("see you at 10");
    fireEvent.submit(input.closest("form")!);
    expect(sent().at(-1)).toEqual({ type: "chat.edit", id: 3, body: "see you at 10" });

    setEditing(line(3, "x", { username: "alice" }));
    fireEvent.keyDown(input, { key: "Escape" });
    expect(editing()).toBeNull();
    expect(input.value).toBe("");
  });

  it("asks Chat for the last line on Up in an empty field only", () => {
    const editLast = vi.fn(() => true);
    const { input, type } = mount([], editLast);
    type("draft");
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(editLast).not.toHaveBeenCalled();
    type("");
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(editLast).toHaveBeenCalledOnce();
  });

  it("hints typing at most every three seconds", () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const { type } = mount();
    const typing = () => sent().filter((m) => m.type === "chat.typing").length;
    type("h");
    type("he");
    expect(typing()).toBe(1);
    vi.setSystemTime(Date.now() + 3_000);
    type("hel");
    expect(typing()).toBe(2);
    type("   ");
    expect(typing()).toBe(2);
  });
});
