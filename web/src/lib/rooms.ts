// REST calls for rooms, members, bans and invites.
import { api } from "~/lib/api";
import type { Ban, Directory, Invite, InviteLink, JoinPreview, Member, Mute, Room, UpcomingRoom, Visibility } from "~/lib/types";

const json = (body: unknown) => ({ method: "POST", body: JSON.stringify(body) });

export const rooms = {
  create: (input: {
    name: string;
    slug?: string;
    visibility: Visibility;
    description?: string;
    scheduledAt?: string;
    firstUrl?: string;
    settings?: { voteMode?: boolean; viewersCanAdd?: boolean };
    invites?: string[];
  }) => api<Room & { warnings?: string[] }>("/api/v1/rooms", json(input)),
  get: (slug: string) => api<Room>(`/api/v1/rooms/${slug}`),
  update: (slug: string, patch: { name?: string; slug?: string; visibility?: Visibility; description?: string; scheduledAt?: string | null }) =>
    api<Room>(`/api/v1/rooms/${slug}`, { method: "PATCH", body: JSON.stringify(patch) }),
  remove: (slug: string) => api<void>(`/api/v1/rooms/${slug}`, { method: "DELETE" }),
  leave: (slug: string, username: string) => api<void>(`/api/v1/rooms/${slug}/members/${username}`, { method: "DELETE" }),
  transfer: (slug: string, username: string) => api<void>(`/api/v1/rooms/${slug}/owner`, json({ username })),
  star: (slug: string, on: boolean) => api<void>(`/api/v1/rooms/${slug}/star`, { method: on ? "PUT" : "DELETE" }),
  /** @deprecated superseded by `directory({ mine: true })`; the endpoint stays for compatibility. */
  mine: () => api<Room[]>("/api/v1/me/rooms"),
  upcoming: () => api<UpcomingRoom[]>("/api/v1/me/upcoming"),
  directory: (params: { q?: string; live?: boolean; private?: boolean; mine?: boolean; starred?: boolean; upcoming?: boolean; sort?: string; page?: number; perPage?: number }) => {
    const qs = new URLSearchParams();
    if (params.q) qs.set("q", params.q);
    if (params.sort && params.sort !== "active") qs.set("sort", params.sort);
    if (params.live) qs.set("live", "1");
    if (params.private) qs.set("private", "1");
    if (params.mine) qs.set("mine", "1");
    if (params.starred) qs.set("starred", "1");
    if (params.upcoming) qs.set("upcoming", "1");
    if (params.page && params.page > 1) qs.set("page", String(params.page));
    if (params.perPage) qs.set("perPage", String(params.perPage));
    const suffix = qs.toString();
    return api<Directory>(`/api/v1/rooms${suffix ? `?${suffix}` : ""}`);
  },

  members: (slug: string) => api<Member[]>(`/api/v1/rooms/${slug}/members`),
  addModerator: (slug: string, username: string) =>
    api<void>(`/api/v1/rooms/${slug}/moderators/${username}`, { method: "PUT" }),
  removeModerator: (slug: string, username: string) =>
    api<void>(`/api/v1/rooms/${slug}/moderators/${username}`, { method: "DELETE" }),
  removeMember: (slug: string, username: string) =>
    api<void>(`/api/v1/rooms/${slug}/members/${username}`, { method: "DELETE" }),

  bans: (slug: string) => api<Ban[]>(`/api/v1/rooms/${slug}/bans`),
  ban: (slug: string, username: string, reason: string) =>
    api<void>(`/api/v1/rooms/${slug}/bans/${username}`, { method: "PUT", body: JSON.stringify({ reason }) }),
  unban: (slug: string, username: string) =>
    api<void>(`/api/v1/rooms/${slug}/bans/${username}`, { method: "DELETE" }),

  mutes: (slug: string) => api<Mute[]>(`/api/v1/rooms/${slug}/mutes`),
  mute: (slug: string, username: string, minutes: number, reason = "") =>
    api<Mute>(`/api/v1/rooms/${slug}/mutes/${username}`, { method: "PUT", body: JSON.stringify({ minutes, reason }) }),
  unmute: (slug: string, username: string) => api<void>(`/api/v1/rooms/${slug}/mutes/${username}`, { method: "DELETE" }),

  invite: (slug: string, username: string) => api<Invite>(`/api/v1/rooms/${slug}/invites`, json({ username })),
};

export const inviteLinks = {
  list: (slug: string) => api<InviteLink[]>(`/api/v1/rooms/${slug}/invite-links`),
  create: (slug: string, input: { expiresIn: string; maxUses: number | null }) =>
    api<InviteLink>(`/api/v1/rooms/${slug}/invite-links`, json(input)),
  revoke: (slug: string, id: string) => api<void>(`/api/v1/rooms/${slug}/invite-links/${id}`, { method: "DELETE" }),
};

export const join = {
  preview: (token: string) => api<JoinPreview>(`/api/v1/join/${token}`),
  accept: (token: string) => api<{ roomSlug: string }>(`/api/v1/join/${token}`, { method: "POST" }),
};

export const invites = {
  mine: () => api<Invite[]>("/api/v1/invites"),
  accept: (id: string) => api<{ roomSlug: string }>(`/api/v1/invites/${id}/accept`, { method: "POST" }),
  decline: (id: string) => api<void>(`/api/v1/invites/${id}/decline`, { method: "POST" }),
};
