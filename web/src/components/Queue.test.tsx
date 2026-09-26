import { fireEvent, render, screen, within } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import Queue from "~/components/Queue";
import type { QueueEntry, ServerMessage, Snapshot } from "~/protocol";
import { createRoomStore, type RoomStore } from "~/store/room";
import { FakeWebSocket } from "~/test/fakeWebSocket";
import { item, snapshot } from "~/test/fixtures";

let room: RoomStore;
const ws = () => FakeWebSocket.all.at(-1)!;
const deliver = (m: ServerMessage) => ws().deliver(m);
const sent = () => ws().sent.map((s) => JSON.parse(s) as Record<string, unknown>).filter((m) => m.type !== "ping").map(({ ref: _ref, ...m }) => m);

function mount(queue: QueueEntry[], opts: { me?: string | null; role?: "owner" | "member"; settings?: Partial<Snapshot["room"]["settings"]>; played?: QueueEntry[] } = {}) {
  render(() => {
    room = createRoomStore("movie-night");
    return <Queue room={room} />;
  });
  ws().accept();
  const snap = snapshot(queue);
  snap.room.settings = { ...snap.room.settings, ...opts.settings };
  snap.played = opts.played ?? [];
  deliver({ type: "welcome", me: opts.me === undefined ? "alice" : opts.me, role: opts.role ?? "owner", snapshot: snap, messages: [] });
}

const row = (title: string) => screen.getByText(title).closest("li")!;

describe("Queue", () => {
  beforeEach(() => {
    FakeWebSocket.all = [];
    vi.stubGlobal("WebSocket", FakeWebSocket);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("lets moderators reorder, jump, shuffle and remove", () => {
    mount([item("a", { current: true }), item("b"), item("c")]);
    expect(screen.getByText("Up next")).toBeTruthy();
    // The first waiting item cannot move above the current one.
    expect((within(row("Video b")).getByRole("button", { name: "Move up" }) as HTMLButtonElement).disabled).toBe(true);
    expect((within(row("Video c")).getByRole("button", { name: "Move down" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(within(row("Video c")).getByRole("button", { name: "Move up" }));
    fireEvent.click(within(row("Video b")).getByRole("button", { name: "Move down" }));
    fireEvent.click(within(row("Video c")).getByRole("button", { name: "play" }));
    fireEvent.click(screen.getByRole("button", { name: "Shuffle" }));
    fireEvent.click(within(row("Video b")).getByRole("button", { name: "Remove" }));
    expect(sent()).toEqual([
      { type: "queue.move", itemId: "c", afterId: "a" },
      { type: "queue.move", itemId: "b", afterId: "c" },
      { type: "jump", itemId: "c" },
      { type: "queue.shuffle" },
      { type: "queue.remove", itemId: "b" },
    ]);
  });

  it("asks before clearing the queue", () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    mount([item("a", { current: true }), item("b"), item("c")]);
    const clear = within(screen.getByText("Up next").closest("h2")!).getByRole("button", { name: "Clear" });
    fireEvent.click(clear);
    fireEvent.click(clear);
    expect(confirm).toHaveBeenCalledWith("Remove 2 waiting videos from the queue?");
    expect(sent()).toEqual([{ type: "queue.clear" }]);
  });

  it("gives members their own remove button only", () => {
    mount([item("a", { current: true, addedBy: "bob" }), item("b", { addedBy: "bob" }), item("c", { addedBy: "alice" })], { role: "member" });
    expect(within(row("Video b")).queryByRole("button", { name: "Remove" })).toBeNull();
    expect(within(row("Video b")).queryByRole("button", { name: "Move up" })).toBeNull();
    expect(within(row("Video c")).getByRole("button", { name: "Remove" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Shuffle" })).toBeNull();
  });

  it("switches to voting in vote mode", () => {
    mount([item("a", { current: true }), item("b", { votes: 2, voted: true })], { settings: { voteMode: true } });
    const vote = within(row("Video b")).getByRole("button", { name: "Vote up (2)" });
    expect(vote.getAttribute("aria-pressed")).toBe("true");
    expect(within(row("Video b")).queryByRole("button", { name: "Move up" })).toBeNull();
    fireEvent.click(vote);
    expect(sent()).toEqual([{ type: "queue.vote", itemId: "b" }]);
  });

  it("says when the fair queue takes turns and hides manual ordering", () => {
    mount([item("a", { current: true }), item("b"), item("c")], { settings: { fairQueue: true } });
    expect(screen.getByText("· taking turns")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Shuffle" })).toBeNull();
    expect(within(row("Video b")).queryByRole("button", { name: "Move up" })).toBeNull();
  });

  it("shows progress, retries failed items and lists pending adds", () => {
    mount([
      item("a", { current: true }),
      item("b", { media: { ...item("b").media, status: "downloading", progress: 0.5, speedBps: 2048 } }),
      item("c", { media: { ...item("c").media, status: "failed", error: "gone" } }),
    ]);
    expect(within(row("Video b")).getByText("50% · 2 KB/s")).toBeTruthy();
    fireEvent.click(within(row("Video c")).getByRole("button", { name: "retry" }));
    expect(sent()).toEqual([{ type: "queue.retry", itemId: "c" }]);
    room.commands.add("https://youtu.be/new", { next: true });
    expect(screen.getByText("adding next…")).toBeTruthy();
  });

  it("offers played videos again", () => {
    mount([item("a", { current: true })], { played: [item("old", { playedMs: 1 })] });
    fireEvent.click(screen.getByRole("button", { name: "play again" }));
    expect(sent()).toEqual([{ type: "queue.replay", itemId: "old" }]);
  });

  it("is empty with a hint", () => {
    mount([], { me: null, role: "member" });
    expect(screen.getByText("Nothing queued yet.")).toBeTruthy();
  });
});
