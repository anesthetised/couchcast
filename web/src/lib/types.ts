// JSON shapes returned by the REST API. Keep in sync with the Go response
// types in internal/apihttp.

export type Role = "user" | "admin";

export interface User {
  id: string;
  username: string;
  role: Role;
  createdAt: string;
  avatarColor?: AvatarColor;
}

// Palette keys shared with the server (entity.AvatarColors); the tokens
// live in styles.css as --c-<key>.
export const AVATAR_COLORS = ["amber", "coral", "rose", "violet", "sky", "teal", "lime", "slate"] as const;
export type AvatarColor = (typeof AVATAR_COLORS)[number];

// avatarClass picks the user's colour, or a stable one from the name so
// people who never chose still look distinct.
export function avatarClass(username: string, color?: string): string {
  if (color) return `c-${color}`;
  let h = 0;
  for (const ch of username.toLowerCase()) h = (h * 31 + ch.charCodeAt(0)) >>> 0;
  return `c-${AVATAR_COLORS[h % AVATAR_COLORS.length]}`;
}

export type Visibility = "public" | "private";
export type RoomRole = "owner" | "moderator" | "member";

export interface Settings {
  voteMode: boolean;
  skipThreshold: number;
  viewersCanAdd: boolean;
  loop: boolean;
  slowModeSec: number;
  pauseWhenEmpty: boolean;
  waitForBuffering: boolean;
  fairQueue: boolean;
}

export interface Room {
  id: string;
  slug: string;
  name: string;
  visibility: Visibility;
  settings: Settings;
  owner: string;
  description: string;
  memberCount: number;
  myRole?: RoomRole;
  starred: boolean;
  scheduledAt: string | null;
  createdAt: string;
}

// UpcomingRoom is a member room with a start announced soon.
export interface UpcomingRoom {
  slug: string;
  name: string;
  scheduledAt: string;
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

export interface Mute {
  username: string;
  reason: string;
  until: string;
  createdAt: string;
}

export interface InviteLink {
  id: string;
  url?: string; // only on creation
  createdBy: string;
  expiresAt: string | null;
  maxUses: number | null;
  uses: number;
  revokedAt: string | null;
  createdAt: string;
}

export interface JoinPreview {
  roomSlug: string;
  roomName: string;
  valid: boolean;
  reason?: string;
  member: boolean;
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
  description?: string;
  myRole?: RoomRole;
  viewers: number;
  memberCount: number;
  live: boolean;
  starred: boolean;
  scheduledMs?: number;
  lastActiveMs: number;
  media: {
    id: string;
    title: string;
    thumbnailUrl: string;
    durationMs: number;
    manifest?: string;
    token?: string;
  } | null;
  playback: { playing: boolean; positionMs: number; atServerMs: number; rate: number } | null;
}

export interface Directory {
  serverNowMs: number;
  page: number;
  perPage: number;
  total: number;
  rooms: DirectoryRoom[];
}

export const isModerator = (role?: RoomRole) => role === "owner" || role === "moderator";
