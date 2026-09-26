import { fireEvent, render, screen, waitFor } from "@solidjs/testing-library";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { QueueEntry, ServerMessage } from "~/protocol";
import { createRoomStore, type RoomStore } from "~/store/room";
import { FakeWebSocket } from "~/test/fakeWebSocket";
import { item, snapshot } from "~/test/fixtures";

// FakePlayer stands in for the Shaka wrapper: it records calls and lets
// a test raise the events Shaka would.
const players: FakePlayer[] = [];
class FakePlayer {
  calls: [string, ...unknown[]][] = [];
  onBuffering: (b: boolean) => void = () => {};
  onTracks: (t: { height: number; width: number; bandwidth: number; active: boolean }[], active: number | null) => void = () => {};
  onError: (m: string) => void = () => {};
  constructor(public video: HTMLVideoElement) {
    players.push(this);
  }
  attach = () => (this.calls.push(["attach"]), Promise.resolve());
  load = (manifest: string, token: string, startMs: number) => (this.calls.push(["load", manifest, token, startMs]), Promise.resolve());
  unload = () => (this.calls.push(["unload"]), Promise.resolve());
  preload = () => Promise.resolve();
  setTokenFor = (manifest: string, token: string) => void this.calls.push(["setTokenFor", manifest, token]);
  addSubtitles = (manifest: string, subs: unknown) => (this.calls.push(["addSubtitles", manifest, subs]), Promise.resolve());
  selectSubtitle = (lang: string | null) => void this.calls.push(["selectSubtitle", lang]);
  selectQuality = (h: number | null) => void this.calls.push(["selectQuality", h]);
  stats = () => ({});
  destroy = () => void this.calls.push(["destroy"]);
}
vi.mock("~/lib/player", () => ({ Player: FakePlayer }));

const { default: Player } = await import("~/components/Player");

let room: RoomStore;
const ws = () => FakeWebSocket.all.at(-1)!;
const deliver = (m: ServerMessage) => ws().deliver(m);
const sent = () =>
  ws()
    .sent.map((s) => JSON.parse(s) as Record<string, unknown>)
    .filter((m) => m.type !== "ping" && m.type !== "report")
    .map(({ ref: _ref, ...m }) => m);
const player = () => players.at(-1)!;

const video = (over: Partial<QueueEntry["media"]> = {}): QueueEntry =>
  item("a", {
    current: true,
    media: {
      ...item("a").media,
      durationMs: 600_000,
      manifest: "/media/m-a/manifest.mpd",
      token: "tok",
      subtitles: [{ lang: "en", name: "English" }, { lang: "de", name: "Deutsch" }],
      chapters: [
        { startMs: 0, endMs: 60_000, title: "Intro" },
        { startMs: 60_000, endMs: 600_000, title: "Main part" },
      ],
      ...over,
    },
  });

function mount(role: "owner" | "member" = "owner", entry = video()) {
  render(() => {
    room = createRoomStore("movie-night");
    return <Player room={room} />;
  });
  ws().accept();
  const snap = snapshot([entry]);
  snap.playback = { itemId: entry.id, playing: false, positionMs: 30_000, atServerMs: Date.now(), rate: 1, seq: 1 };
  deliver({ type: "welcome", me: "alice", role, snapshot: snap, messages: [] });
}

const key = (k: string, init: KeyboardEventInit = {}) => fireEvent.keyDown(document, { key: k, ...init });

describe("Player", () => {
  beforeEach(() => {
    FakeWebSocket.all = [];
    players.length = 0;
    localStorage.clear();
    vi.stubGlobal("WebSocket", FakeWebSocket);
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
  });
  afterEach(() => vi.unstubAllGlobals());

  it("loads the current video with its token, subtitles and saved quality", async () => {
    localStorage.setItem("couchcast.quality", "720");
    mount();
    await waitFor(() => expect(player().calls.some(([c]) => c === "load")).toBe(true));
    const load = player().calls.find(([c]) => c === "load")!;
    expect(load.slice(1, 3)).toEqual(["/media/m-a/manifest.mpd", "tok"]);
    expect(load[3]).toBeGreaterThanOrEqual(30_000); // the room's position
    await waitFor(() => expect(player().calls).toContainEqual(["selectQuality", 720]));
    expect(player().calls.some(([c]) => c === "addSubtitles")).toBe(true);
  });

  it("offers the qualities Shaka reports and remembers the choice", async () => {
    mount();
    await waitFor(() => expect(players).toHaveLength(1));
    player().onTracks(
      [
        { height: 1080, width: 1920, bandwidth: 1, active: true },
        { height: 480, width: 854, bandwidth: 1, active: false },
      ],
      1080,
    );
    const select = (await screen.findByRole("option", { name: "1080p" })).closest("select")!;
    expect(screen.getByRole("option", { name: "Auto (1080p)" })).toBeTruthy();
    fireEvent.change(select, { target: { value: "480" } });
    expect(player().calls).toContainEqual(["selectQuality", 480]);
    expect(localStorage.getItem("couchcast.quality")).toBe("480");
  });

  it("switches subtitles from the menu and with C", async () => {
    mount();
    const cc = await screen.findByLabelText("Subtitles");
    fireEvent.change(cc, { target: { value: "de" } });
    expect(player().calls).toContainEqual(["selectSubtitle", "de"]);
    key("c");
    expect(player().calls.at(-1)).toEqual(["selectSubtitle", null]);
    key("c");
    expect(player().calls.at(-1)).toEqual(["selectSubtitle", "en"]);
  });

  it("drives the room with hotkeys when the viewer may control it", async () => {
    mount("owner");
    await screen.findByLabelText("Position");
    key(" ");
    key("ArrowRight");
    key("n");
    key(">");
    key("]");
    expect(sent().map((m) => m.type)).toEqual(["play", "seek", "next", "rate.set", "seek"]);
    expect(sent()[3]).toEqual({ type: "rate.set", rate: 1.25 });
    expect(sent()[4]).toEqual({ type: "seek", positionMs: 60_000 }); // the next chapter
  });

  it("ignores hotkeys while typing and for viewers without control", async () => {
    mount("member");
    const play = (await screen.findByLabelText("Position")).closest(".controls")!.querySelector<HTMLButtonElement>(".transport button.icon")!;
    expect(play.disabled).toBe(true);
    key(" ");
    key("ArrowRight");
    key("n");
    const input = document.createElement("input");
    document.body.append(input);
    fireEvent.keyDown(input, { key: "m" });
    expect(sent()).toEqual([]);
    input.remove();
  });

  it("mutes with M and keeps the volume", async () => {
    mount();
    await screen.findByLabelText("Position");
    const v = document.querySelector("video")!;
    const before = v.muted;
    key("m");
    expect(v.muted).toBe(!before);
    expect(localStorage.getItem("couchcast.muted")).toBe(String(!before));
    fireEvent.input(screen.getByLabelText("Volume"), { target: { value: "0.4" } });
    expect(localStorage.getItem("couchcast.volume")).toBe("0.4");
  });

  it("lists chapters and jumps to one", async () => {
    mount();
    fireEvent.click(await screen.findByLabelText("Chapters"));
    fireEvent.click(screen.getByRole("menuitem", { name: /Main part/ }));
    expect(sent()).toEqual([{ type: "seek", positionMs: 60_000 }]);
  });

  it("shows a player error with a way to report it", async () => {
    mount();
    await waitFor(() => expect(players).toHaveLength(1));
    player().onError("Player error 4032");
    expect(await screen.findByText("Player error 4032")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Report this" })).toBeTruthy();
  });

  it("asks for a tap when the browser blocks autoplay", async () => {
    vi.mocked(HTMLMediaElement.prototype.play).mockRejectedValue(new DOMException("no gesture", "NotAllowedError"));
    render(() => {
      room = createRoomStore("movie-night");
      return <Player room={room} />;
    });
    ws().accept();
    const entry = video();
    const snap = snapshot([entry]);
    snap.playback = { itemId: entry.id, playing: true, positionMs: 1_000, atServerMs: Date.now(), rate: 1, seq: 1 };
    deliver({ type: "welcome", me: "alice", role: "member", snapshot: snap, messages: [] });
    const v = document.querySelector("video")!;
    Object.defineProperty(v, "readyState", { value: 4, configurable: true });
    expect(await screen.findByText("Tap to play", {}, { timeout: 3000 })).toBeTruthy();
  });

  it("releases the player when it goes away", async () => {
    const { unmount } = render(() => {
      room = createRoomStore("movie-night");
      return <Player room={room} />;
    });
    await waitFor(() => expect(players).toHaveLength(1));
    unmount();
    expect(player().calls.at(-1)).toEqual(["destroy"]);
  });
});
