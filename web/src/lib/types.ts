// JSON shapes returned by the REST API. Keep in sync with the Go response
// types in internal/apihttp.

export type Role = "user" | "admin";

export interface User {
  id: string;
  username: string;
  role: Role;
  createdAt: string;
}

export type Visibility = "public" | "private";
export type RoomRole = "owner" | "moderator" | "member";

export interface Settings {
  voteMode: boolean;
  skipThreshold: number;
  viewersCanAdd: boolean;
}

export interface Room {
  id: string;
  slug: string;
  name: string;
  visibility: Visibility;
  settings: Settings;
  owner: string;
  memberCount: number;
  myRole?: RoomRole;
  createdAt: string;
}

export interface Member {
  username: string;
  role: RoomRole;
  joinedAt: string;
}

export interface Ban {
  username: string;
  reason: string;
  createdAt: string;
}

export interface Invite {
  id: string;
  roomSlug: string;
  roomName: string;
  inviter: string;
  status: "pending" | "accepted" | "declined";
  createdAt: string;
}

export interface DirectoryRoom {
  slug: string;
  name: string;
  owner: string;
  visibility: Visibility;
  myRole?: RoomRole;
  viewers: number;
  memberCount: number;
  live: boolean;
  media: {
    id: string;
    title: string;
    thumbnailUrl: string;
    durationMs: number;
    manifest?: string;
    token?: string;
  } | null;
  playback: { playing: boolean; positionMs: number; atServerMs: number } | null;
}

export interface Directory {
  serverNowMs: number;
  page: number;
  perPage: number;
  total: number;
  rooms: DirectoryRoom[];
}

export const isModerator = (role?: RoomRole) => role === "owner" || role === "moderator";
