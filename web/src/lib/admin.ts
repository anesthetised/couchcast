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

export interface AuditPage {
  entries: AuditEntry[];
  nextBefore: number; // 0 at the end
}

export interface StorageMedia {
  id: string;
  title: string;
  sourceUrl: string;
  sizeBytes: number;
  queued: boolean;
  lastAccessedAt: string;
  createdAt: string;
}

export interface Storage {
  totalBytes: number;
  budgetBytes: number;
  media: StorageMedia[];
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
  audit: (params: { action?: string; actor?: string; before?: number } = {}) => {
    const qs = new URLSearchParams();
    if (params.action) qs.set("action", params.action);
    if (params.actor) qs.set("actor", params.actor);
    if (params.before) qs.set("before", String(params.before));
    const suffix = qs.toString();
    return api<AuditPage>(`/api/v1/admin/audit${suffix ? `?${suffix}` : ""}`);
  },
  storage: () => api<Storage>("/api/v1/admin/storage"),
  evictMedia: (mediaId: string) => api<void>(`/api/v1/admin/media/${mediaId}/evict`, post()),
  evictStale: (olderThanDays: number) => api<{ removed: number; bytes: number }>("/api/v1/admin/storage/evict", post({ olderThanDays })),
};
