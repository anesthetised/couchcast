// Wire types for the room WebSocket. Mirrors internal/protocol in Go.

import type { RoomRole, Settings, Visibility } from "~/lib/types";

export type MediaStatus = "queued" | "probing" | "downloading" | "packaging" | "uploading" | "ready" | "failed";

export interface Rendition {
  id: string;
  height: number;
  width: number;
  codec: string;
  bitrate: number;
}

export interface Subtitle {
  lang: string;
  name: string;
  auto?: boolean;
}

export interface Chapter {
  startMs: number;
  endMs: number;
  title: string;
}

export interface MediaInfo {
  id: string;
  title: string;
  durationMs: number;
  thumbnailUrl: string;
  status: MediaStatus;
  progress: number;
  error?: string;
  renditions: Rendition[];
  subtitles?: Subtitle[];
  chapters?: Chapter[];
  manifest?: string;
  token?: string;
  sourceUrl: string;
}

export interface QueueEntry {
  id: string;
  media: MediaInfo;
  addedBy: string;
  votes: number;
  voted: boolean;
  current: boolean;
  playedMs?: number; // history entries only
}

export interface Presence {
  username: string;
  color?: string;
  role?: RoomRole;
  buffering: boolean;
}

export interface RoomInfo {
  id: string;
  slug: string;
  name: string;
  visibility: Visibility;
  settings: Settings;
  owner: string;
  description?: string;
}

export interface Playback {
  itemId: string | null;
  playing: boolean;
  positionMs: number;
  atServerMs: number;
  rate: number;
  seq: number;
}

export interface Snapshot {
  type: "room.state";
  room: RoomInfo;
  playback: Playback;
  queue: QueueEntry[];
  played: QueueEntry[]; // newest first
  members: Presence[];
  guests: number;
  skipVotes: number;
  skipVoted: boolean;
  skipNeeded: number;
}

export interface ChatMessage {
  type?: "chat.message";
  id: number;
  username?: string;
  color?: string;
  body: string;
  system?: boolean;
  createdMs: number;
}

export interface Welcome {
  type: "welcome";
  me: string | null;
  role?: RoomRole;
  snapshot: Snapshot;
  messages: ChatMessage[];
}

export interface Pong {
  type: "pong";
  t0: number;
  t1: number;
}

export interface PlaybackMessage extends Playback {
  type: "playback";
}

export interface ChatDeleted {
  type: "chat.deleted";
  id: number;
}

export interface ChatCleared {
  type: "chat.cleared";
}

export interface Typing {
  type: "typing";
  username: string;
}

export interface Reaction {
  type: "reaction";
  username: string;
  emoji: string;
}

export interface Kicked {
  type: "kicked";
  reason: string;
}

export interface ErrorMessage {
  type: "error";
  code: "forbidden" | "invalid" | "not_found" | "internal" | "rate_limited" | "duplicate";
  message: string;
}

export type ServerMessage =
  | Welcome
  | Pong
  | Snapshot
  | PlaybackMessage
  | ChatMessage
  | ChatDeleted
  | ChatCleared
  | Typing
  | Reaction
  | Kicked
  | ErrorMessage;

export type ClientMessage =
  | { type: "ping"; t0: number }
  | { type: "play" }
  | { type: "pause" }
  | { type: "seek"; positionMs: number }
  | { type: "rate.set"; rate: number }
  | { type: "next" }
  | { type: "jump"; itemId: string }
  | { type: "queue.add"; url: string; next?: boolean; force?: boolean }
  | { type: "queue.replay"; itemId: string }
  | { type: "queue.clearPlayed" }
  | { type: "queue.clear" }
  | { type: "queue.shuffle" }
  | { type: "queue.remove"; itemId: string }
  | { type: "queue.move"; itemId: string; afterId: string | null }
  | { type: "queue.retry"; itemId: string }
  | { type: "queue.vote"; itemId: string }
  | { type: "skip.vote" }
  | { type: "session.end" }
  | { type: "settings.set"; voteMode?: boolean; skipThreshold?: number; viewersCanAdd?: boolean; loop?: boolean; slowModeSec?: number }
  | { type: "chat.send"; body: string }
  | { type: "chat.delete"; id: number }
  | { type: "chat.typing" }
  | { type: "chat.clear" }
  | { type: "react"; emoji: string }
  | { type: "report"; state: "playing" | "buffering" | "ended"; positionMs: number };
