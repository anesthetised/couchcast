// REST calls for rooms, members, bans and invites.
import { api } from "~/lib/api";
import type { Ban, Directory, Invite, Member, Room, Visibility } from "~/lib/types";

const json = (body: unknown) => ({ method: "POST", body: JSON.stringify(body) });

export const rooms = {
  create: (input: {
    name: string;
    slug?: string;
    visibility: Visibility;
    firstUrl?: string;
    settings?: { voteMode?: boolean; viewersCanAdd?: boolean };
    invites?: string[];
  }) => api<Room & { warnings?: string[] }>("/api/v1/rooms", json(input)),
  get: (slug: string) => api<Room>(`/api/v1/rooms/${slug}`),
  update: (slug: string, patch: { name?: string; slug?: string; visibility?: Visibility }) =>
    api<Room>(`/api/v1/rooms/${slug}`, { method: "PATCH", body: JSON.stringify(patch) }),
  remove: (slug: string) => api<void>(`/api/v1/rooms/${slug}`, { method: "DELETE" }),
  /** @deprecated superseded by `directory({ mine: true })`; the endpoint stays for compatibility. */
  mine: () => api<Room[]>("/api/v1/me/rooms"),
  directory: (params: { q?: string; live?: boolean; private?: boolean; mine?: boolean; page?: number; perPage?: number }) => {
    const qs = new URLSearchParams();
    if (params.q) qs.set("q", params.q);
    if (params.live) qs.set("live", "1");
    if (params.private) qs.set("private", "1");
    if (params.mine) qs.set("mine", "1");
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

  invite: (slug: string, username: string) => api<Invite>(`/api/v1/rooms/${slug}/invites`, json({ username })),
};

export const invites = {
  mine: () => api<Invite[]>("/api/v1/invites"),
  accept: (id: string) => api<{ roomSlug: string }>(`/api/v1/invites/${id}/accept`, { method: "POST" }),
  decline: (id: string) => api<void>(`/api/v1/invites/${id}/decline`, { method: "POST" }),
};
