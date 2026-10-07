import { createRoot } from "solid-js";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { QueueEntry } from "~/protocol";
import { createRoomStore, type RoomStore } from "~/store/room";
import { players } from "~/test/fakePlayer";
import { FakeWebSocket } from "~/test/fakeWebSocket";
import { item, snapshot } from "~/test/fixtures";

vi.mock("~/lib/player", async () => ({ Player: (await import("~/test/fakePlayer")).FakePlayer }));
const { createPlayback } = await import("~/lib/playback");

const ws = () => FakeWebSocket.all.at(-1)!;
const player = () => players.at(-1)!;
const calls = (name: string) => player().calls.filter(([c]) => c === name);
const flush = () => new Promise((r) => setTimeout(r, 0));

const ready = (id: string, token = "tok"): QueueEntry =>
  item(id, { current: true, media: { ...item(id).media, status: "ready", durationMs: 600_000, manifest: `/media/m-${id}/manifest.mpd`, token } });

// The room shows entry as its current item (or nothing).
function show(entry: QueueEntry | null, welcome = false) {
  const snap = snapshot(entry ? [entry] : []);
  snap.playback = { itemId: entry?.id ?? null, playing: false, positionMs: 30_000, atServerMs: Date.now(), rate: 1, seq: 1 };
  ws().deliver(welcome ? { type: "welcome", me: "alice", role: "owner", snapshot: snap, messages: [] } : snap);
}

describe("createPlayback", () => {
  let room: RoomStore;
  let disposeRoom: () => void;
  let video: HTMLVideoElement;

  beforeEach(() => {
    FakeWebSocket.all = [];
    players.length = 0;
    vi.stubGlobal("WebSocket", FakeWebSocket);
    vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined);
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(() => {});
    createRoot((d) => {
      disposeRoom = d;
      room = createRoomStore("movie-night");
    });
    ws().accept();
    video = document.createElement("video");
  });
  afterEach(() => {
    disposeRoom();
    vi.unstubAllGlobals();
    vi.useRealTimers();
  });

  const mount = (prefs = { quality: () => 720 as number | null, subtitle: () => null as string | null }) =>
    createRoot((dispose) => ({ dispose, playback: createPlayback(() => video, room, prefs) }));

  it("loads the current item at the room's position with the saved quality", async () => {
    show(ready("a"), true);
    const { playback, dispose } = mount();
    await flush();
    const [, manifest, token, start] = calls("load")[0]!;
    expect([manifest, token]).toEqual(["/media/m-a/manifest.mpd", "tok"]);
    expect(start).toBeGreaterThanOrEqual(30_000);
    expect(calls("selectQuality")).toEqual([["selectQuality", 720]]);
    expect(playback.loadedMediaId()).toBe("m-a");
    dispose();
  });

  it("refreshes the token of the loaded item instead of reloading it", async () => {
    show(ready("a"), true);
    const { dispose } = mount();
    await flush();
    show(ready("a", "fresh"));
    await flush();
    expect(calls("load")).toHaveLength(1);
    expect(calls("setTokenFor")).toEqual([["setTokenFor", "/media/m-a/manifest.mpd", "fresh"]]);
    dispose();
  });

  it("follows item changes: loads the next, unloads when nothing plays", async () => {
    show(ready("a"), true);
    const { playback, dispose } = mount();
    await flush();
    show(ready("b"));
    await flush();
    expect(calls("load").map((c) => c[1])).toEqual(["/media/m-a/manifest.mpd", "/media/m-b/manifest.mpd"]);
    expect(playback.loadedMediaId()).toBe("m-b");
    show(null);
    await flush();
    expect(calls("unload")).toHaveLength(1);
    expect(playback.loadedMediaId()).toBeNull();
    dispose();
  });

  it("drops a load that a newer one interrupted without an error", async () => {
    show(ready("a"), true);
    const { playback, dispose } = mount();
    await flush();
    let interrupt!: () => void;
    const fake = player();
    const load = fake.load;
    fake.load = (manifest, token, start) => {
      if (manifest.includes("m-b")) {
        fake.calls.push(["load", manifest, token, start]);
        return new Promise((_, reject) => (interrupt = () => reject(Object.assign(new Error("interrupted"), { code: 7000 }))));
      }
      return load(manifest, token, start);
    };
    show(ready("b"));
    await flush();
    show(ready("c"));
    interrupt();
    await flush();
    expect(playback.error()).toBeNull();
    expect(playback.loadedMediaId()).toBe("m-c");
    dispose();
  });

  it("reports other load failures", async () => {
    show(ready("a"), true);
    players.length = 0;
    const { playback, dispose } = mount();
    player().load = () => Promise.reject(Object.assign(new Error("boom"), { code: 4032 }));
    show(ready("b"));
    await flush();
    expect(playback.error()).toBe("Player error 4032");
    dispose();
  });

  it("leaves no timers or listeners behind", async () => {
    vi.useFakeTimers();
    const added: string[] = [];
    const removed: string[] = [];
    vi.spyOn(video, "addEventListener").mockImplementation((t) => void added.push(t));
    vi.spyOn(video, "removeEventListener").mockImplementation((t) => void removed.push(t));
    show(ready("a"), true);
    const before = vi.getTimerCount();

    const { dispose } = mount();
    await vi.advanceTimersByTimeAsync(0);
    expect(vi.getTimerCount()).toBeGreaterThan(before);
    dispose();

    expect(vi.getTimerCount()).toBe(before);
    // Every listener added is removed (the synchronizer also removes once
    // before it starts).
    expect(new Set(removed)).toEqual(new Set(added));
    expect(player().calls.at(-1)).toEqual(["destroy"]);
  });
});
