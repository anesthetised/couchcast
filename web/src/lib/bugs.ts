import { createSignal } from "solid-js";

import { api } from "~/lib/api";

export type BugCategory = "playback" | "sync" | "subtitles" | "chat" | "other";

export const BUG_CATEGORIES: { id: BugCategory; label: string }[] = [
  { id: "playback", label: "Playback" },
  { id: "sync", label: "Sync" },
  { id: "subtitles", label: "Subtitles" },
  { id: "chat", label: "Chat" },
  { id: "other", label: "Other" },
];

export interface BugReport {
  id: string;
  author: string;
  roomSlug?: string;
  mediaId?: string;
  mediaTitle?: string;
  category: BugCategory;
  description: string;
  client: Record<string, unknown>;
  server: Record<string, unknown>;
  hasFrame: boolean;
  createdAt: string;
  resolvedAt: string | null;
  note: string;
}

export interface BugReportPage {
  reports: BugReport[];
  nextBefore: string; // "" at the end
}

const post = (body?: unknown) => ({ method: "POST", body: body === undefined ? undefined : JSON.stringify(body) });

export const bugs = {
  create: (input: { category: BugCategory; description: string; roomSlug?: string; mediaId?: string; client?: unknown; frame?: string }) =>
    api<{ id: string }>("/api/v1/bug-reports", post(input)),
  list: (status: "open" | "resolved", before?: string) => {
    const qs = new URLSearchParams({ status });
    if (before) qs.set("before", before);
    return api<BugReportPage>(`/api/v1/admin/bug-reports?${qs.toString()}`);
  },
  get: (id: string) => api<BugReport>(`/api/v1/admin/bug-reports/${id}`),
  frameUrl: (id: string) => `/api/v1/admin/bug-reports/${id}/frame`,
  resolve: (id: string, note: string) => api<void>(`/api/v1/admin/bug-reports/${id}/resolve`, post({ note })),
};

// The dialog is mounted once by the room page; anything (the player, the
// reconnecting strip, a hotkey) opens it with an optional prefill.
export type BugPrefill = { category?: BugCategory; description?: string };

const [request, setRequest] = createSignal<BugPrefill | null>(null);
export const bugRequest = request;
export const openBugReport = (prefill: BugPrefill = {}) => setRequest({ ...prefill });
export const closeBugReport = () => setRequest(null);
