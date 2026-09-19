// REST calls for the administration panel and media reports.
import { api } from "~/lib/api";
import type { Room } from "~/lib/types";

export type ReportReason = "copyright" | "illegal" | "nsfw" | "other";

export interface Stats {
  users: number;
  bannedUsers: number;
  rooms: number;
  privateRooms: number;
  mediaByStatus: Record<string, number>;
  mediaBytes: number;
  pendingJobs: number;
  runningJobs: number;
  failedJobs: number;
  openReports: number;
  roomsLoaded: number;
}

export interface AdminUser {
  id: string;
  username: string;
  role: "user" | "admin";
  banned: boolean;
  bannedReason?: string;
  roomCount: number;
  createdAt: string;
}

export interface ReportedMedia {
  media: { id: string; sourceKey: string; sourceUrl: string; title: string; status: string; sizeBytes: number; thumbnailUrl: string };
  count: number;
  reports: {
    id: string;
    reporter: string;
    roomSlug?: string;
    roomName?: string;
    addedBy?: string;
    reason: ReportReason;
    comment: string;
    createdAt: string;
  }[];
  placements: { roomSlug: string; roomName: string; addedBy: string }[];
}

export interface BlocklistEntry {
  sourceKey: string;
  reason: string;
  createdBy: string;
  createdAt: string;
}

export interface AuditEntry {
  id: number;
  actor: string;
  action: string;
  targetType: string;
  targetId: string;
  roomId: string | null;
  meta: Record<string, unknown>;
  createdAt: string;
}

const post = (body?: unknown) => ({ method: "POST", body: body === undefined ? undefined : JSON.stringify(body) });

export const reports = {
  create: (mediaId: string, reason: ReportReason, comment: string, roomSlug?: string) =>
    api<void>(`/api/v1/media/${mediaId}/reports`, post({ reason, comment, roomSlug })),
};

export const admin = {
  stats: () => api<Stats>("/api/v1/admin/stats"),
  users: (q: string) => api<AdminUser[]>(`/api/v1/admin/users?q=${encodeURIComponent(q)}`),
  ban: (id: string, reason: string) => api<void>(`/api/v1/admin/users/${id}/ban`, post({ reason })),
  unban: (id: string) => api<void>(`/api/v1/admin/users/${id}/unban`, post()),
  rooms: (q: string) => api<Room[]>(`/api/v1/admin/rooms?q=${encodeURIComponent(q)}`),
  deleteRoom: (slug: string) => api<void>(`/api/v1/admin/rooms/${slug}`, { method: "DELETE" }),
  reports: () => api<ReportedMedia[]>("/api/v1/admin/reports"),
  dismiss: (mediaId: string) => api<void>(`/api/v1/admin/reports/${mediaId}/dismiss`, post()),
  deleteMedia: (mediaId: string, reason: string) =>
    api<void>(`/api/v1/admin/media/${mediaId}`, { method: "DELETE", body: JSON.stringify({ reason }) }),
  blocklist: () => api<BlocklistEntry[]>("/api/v1/admin/blocklist"),
  block: (sourceKey: string, reason: string) => api<void>("/api/v1/admin/blocklist", post({ sourceKey, reason })),
  unblock: (sourceKey: string) => api<void>(`/api/v1/admin/blocklist/${sourceKey}`, { method: "DELETE" }),
  audit: () => api<AuditEntry[]>("/api/v1/admin/audit"),
};
