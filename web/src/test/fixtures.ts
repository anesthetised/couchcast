// Protocol fixtures for unit tests.
import type { ChatMessage, QueueEntry, Snapshot } from "~/protocol";

export const item = (id: string, over: Partial<QueueEntry> = {}): QueueEntry => ({
  id,
  addedBy: "alice",
  votes: 0,
  voted: false,
  current: false,
  media: { id: `m-${id}`, title: `Video ${id}`, durationMs: 60_000, thumbnailUrl: "", status: "ready", progress: 1, renditions: [], sourceUrl: `https://youtu.be/${id}` },
  ...over,
});

export const snapshot = (queue: QueueEntry[] = []): Snapshot => ({
  type: "room.state",
  room: {
    id: "room-1",
    slug: "movie-night",
    name: "Movie night",
    visibility: "public",
    owner: "alice",
    settings: { voteMode: false, viewersCanAdd: true } as Snapshot["room"]["settings"],
  },
  playback: { itemId: queue[0]?.id ?? null, playing: false, positionMs: 0, atServerMs: 0, rate: 1, seq: 1 },
  queue,
  played: [],
  members: [{ username: "alice", buffering: false }],
  guests: 0,
  skipVotes: 0,
  skipVoted: false,
  skipNeeded: 1,
});

export const line = (id: number, body: string, over: Partial<ChatMessage> = {}): ChatMessage => ({ type: "chat.message", id, username: "bob", body, createdMs: id, ...over });
