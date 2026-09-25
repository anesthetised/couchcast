import { fireEvent, render, screen, within } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import Chat from "~/components/Chat";
import type { ChatMessage, ServerMessage } from "~/protocol";
import { createRoomStore, type RoomStore } from "~/store/room";
import { FakeWebSocket } from "~/test/fakeWebSocket";
import { line, snapshot } from "~/test/fixtures";

const sys = (id: number, body: string): ChatMessage => ({ id, body, system: true, createdMs: id });

let room: RoomStore;
const ws = () => FakeWebSocket.all.at(-1)!;
const deliver = (m: ServerMessage) => ws().deliver(m);
const sent = () => ws().sent.map((s) => JSON.parse(s) as Record<string, unknown>).filter((m) => m.type !== "ping");

function mount(messages: ChatMessage[], me: string | null = "alice", role: "owner" | "member" = "member") {
  const r = render(() => {
    room = createRoomStore("movie-night");
    return <Chat room={room} />;
  });
  ws().accept();
  deliver({ type: "welcome", me, role, snapshot: snapshot(), messages });
  return r;
}

describe("Chat", () => {
  beforeEach(() => {
    FakeWebSocket.all = [];
    vi.stubGlobal("WebSocket", FakeWebSocket);
    Element.prototype.scrollIntoView = () => {};
  });
  afterEach(() => vi.unstubAllGlobals());

  it("folds three or more system lines, keeping the last one visible", async () => {
    mount([line(1, "hi"), sys(2, "bob joined"), sys(3, "eve joined"), sys(4, "paused"), sys(5, "playing"), line(6, "yo")]);
    const toggle = await screen.findByRole("button", { name: "▸ 3 earlier events" });
    expect(screen.queryByText("bob joined")).toBeNull();
    expect(screen.getByText("playing")).toBeTruthy();
    fireEvent.click(toggle);
    expect(screen.getByText("bob joined")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "▾ hide events" }));
    expect(screen.queryByText("bob joined")).toBeNull();
  });

  it("leaves short runs of system lines alone", async () => {
    mount([sys(1, "bob joined"), sys(2, "eve joined"), line(3, "hi")]);
    await screen.findByText("bob joined");
    expect(screen.queryByRole("button", { name: /earlier events/ })).toBeNull();
  });

  it("sends a message with Enter, expanding shortcodes", async () => {
    mount([]);
    const input = await screen.findByPlaceholderText("Say something");
    fireEvent.input(input, { target: { value: "nice :fire:" } });
    fireEvent.submit(input.closest("form")!);
    expect(sent().at(-1)).toEqual({ type: "chat.send", body: "nice 🔥" });
    expect((input as HTMLInputElement).value).toBe("");
  });

  it("edits one's own recent line in the composer and marks it edited", async () => {
    mount([line(1, "see you at 9", { username: "alice", createdMs: Date.now() }), line(2, "ok", { username: "bob", createdMs: Date.now() })]);
    const mine = (await screen.findByText("see you at 9")).closest("li")!;
    const theirs = screen.getByText("ok").closest("li")!;
    expect(within(theirs).queryByRole("button", { name: "edit" })).toBeNull();
    fireEvent.click(within(mine).getByRole("button", { name: "edit" }));
    const input = screen.getByPlaceholderText("Say something") as HTMLInputElement;
    expect(input.value).toBe("see you at 9");
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
    fireEvent.input(input, { target: { value: "see you at 10" } });
    fireEvent.submit(input.closest("form")!);
    expect(sent().at(-1)).toEqual({ type: "chat.edit", id: 1, body: "see you at 10" });

    deliver({ type: "chat.edited", message: { id: 1, username: "alice", body: "see you at 10", createdMs: Date.now(), editedMs: Date.now() } });
    expect(await screen.findByText("(edited)")).toBeTruthy();
  });

  it("offers Up in an empty field to edit the last own line, Escape to cancel", async () => {
    mount([line(1, "first", { username: "alice", createdMs: Date.now() }), line(2, "second", { username: "alice", createdMs: Date.now() })]);
    const input = (await screen.findByPlaceholderText("Say something")) as HTMLInputElement;
    fireEvent.keyDown(input, { key: "ArrowUp" });
    expect(input.value).toBe("second");
    fireEvent.keyDown(input, { key: "Escape" });
    expect(input.value).toBe("");
    expect(screen.getByRole("button", { name: "Send" })).toBeTruthy();
  });

  it("does not offer editing after the window", async () => {
    mount([line(1, "old", { username: "alice", createdMs: Date.now() - 6 * 60_000 })]);
    const mine = (await screen.findByText("old")).closest("li")!;
    expect(within(mine).queryByRole("button", { name: "edit" })).toBeNull();
  });

  it("replies with a quote and cancels with Escape", async () => {
    mount([line(1, "which one?")]);
    const target = (await screen.findByText("which one?")).closest("li")!;
    fireEvent.click(within(target).getByRole("button", { name: "reply" }));
    expect(screen.getByText("Replying to")).toBeTruthy();
    const input = screen.getByPlaceholderText("Say something");
    fireEvent.input(input, { target: { value: "the first" } });
    fireEvent.submit(input.closest("form")!);
    expect(sent().at(-1)).toEqual({ type: "chat.send", body: "the first", replyTo: 1 });
  });

  it("lets moderators pin and delete anyone's line, members only their own", async () => {
    mount([line(1, "spam"), line(2, "mine", { username: "alice" })]);
    const spam = (await screen.findByText("spam")).closest("li")!;
    expect(within(spam).queryByRole("button", { name: "Delete" })).toBeNull();
    fireEvent.click(within(screen.getByText("mine").closest("li")!).getByRole("button", { name: "Delete" }));
    expect(sent().at(-1)).toEqual({ type: "chat.delete", id: 2 });
  });

  it("shows moderators the pin action", async () => {
    mount([line(1, "rules")], "alice", "owner");
    const rules = (await screen.findByText("rules")).closest("li")!;
    fireEvent.click(within(rules).getByRole("button", { name: "pin" }));
    expect(sent().at(-1)).toEqual({ type: "chat.pin", id: 1 });
  });

  it("asks anonymous viewers to log in", async () => {
    mount([line(1, "hi")], null);
    expect(await screen.findByText("Log in")).toBeTruthy();
    expect(screen.queryByPlaceholderText("Say something")).toBeNull();
  });
});
