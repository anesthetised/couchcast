import { createEffect, createResource, createRoot, onCleanup } from "solid-js";

import { api, ApiError } from "~/lib/api";
import { notify, syncPush } from "~/lib/notify";
import { rooms } from "~/lib/rooms";
import type { Invite, Session, UpcomingRoom, User } from "~/lib/types";

const INVITE_POLL_MS = 60_000;
const REMIND_BEFORE_MS = 10 * 60_000;

export interface Credentials {
  username: string;
  password: string;
}

// Global auth state: the current user, loaded once from /auth/me and
// updated by login/register/logout. Lives outside the component tree so
// every route shares it.
function createAuthStore() {
  const [user, { mutate, refetch }] = createResource<User | null>(
    async () => {
      try {
        return await api<User>("/api/v1/auth/me");
      } catch (err) {
        if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
          return null;
        }
        throw err;
      }
    },
    { initialValue: null },
  );

  async function login(c: Credentials) {
    const u = await api<User>("/api/v1/auth/login", { method: "POST", body: JSON.stringify(c) });
    mutate(u);
    return u;
  }

  async function register(c: Credentials) {
    const u = await api<User>("/api/v1/auth/register", { method: "POST", body: JSON.stringify(c) });
    mutate(u);
    return u;
  }

  async function updateMe(patch: { avatarColor?: string }) {
    const u = await api<User>("/api/v1/me", { method: "PATCH", body: JSON.stringify(patch) });
    mutate(u);
    return u;
  }

  async function changePassword(current: string, next: string) {
    await api<void>("/api/v1/me/password", { method: "POST", body: JSON.stringify({ current, new: next }) });
  }

  async function sessions() {
    return api<Session[]>("/api/v1/me/sessions");
  }

  async function revokeSession(id: string) {
    await api<void>(`/api/v1/me/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
  }

  async function revokeOtherSessions() {
    return api<{ revoked: number }>("/api/v1/me/sessions", { method: "DELETE" });
  }

  async function logout() {
    await api<void>("/api/v1/auth/logout", { method: "POST" });
    mutate(null);
  }

  // Pending invites, for the topbar badge; refetched with the user and on
  // demand by the inbox.
  const [invites, { refetch: refetchInvites, mutate: setInvites }] = createResource(
    () => user()?.id ?? null,
    async (id) => (id ? api<Invite[]>("/api/v1/invites") : []),
    { initialValue: [] },
  );

  // Invites have no live channel: poll while signed in and announce new
  // ones (only after the first load, so the badge does not fire a burst).
  let known: Set<string> | null = null;
  createEffect(() => {
    const list = invites();
    if (known !== null) {
      for (const inv of list) if (!known.has(inv.id)) notify("Room invite", `${inv.inviter} invited you to ${inv.roomName}`, `invite-${inv.id}`);
    }
    known = new Set(list.map((i) => i.id));
  });
  // Announced sessions: the same poll checks the member rooms starting
  // soon and reminds ten minutes before, once per start.
  const remindUpcoming = async () => {
    let list: UpcomingRoom[];
    try {
      list = await rooms.upcoming();
    } catch {
      return;
    }
    const now = Date.now();
    for (const u of list) {
      const at = new Date(u.scheduledAt).getTime();
      if (at - now > REMIND_BEFORE_MS) continue;
      const key = `couchcast.reminded.${u.slug}.${at}`;
      try {
        if (localStorage.getItem(key)) continue;
        localStorage.setItem(key, "1");
      } catch {
        // storage unavailable: remind every poll rather than never
      }
      const min = Math.max(1, Math.round((at - now) / 60_000));
      notify(`${u.name} starts in ${min} min`, "Your watch party is about to begin.", `start-${u.slug}-${at}`);
    }
  };
  createEffect(() => {
    if (!user()) {
      known = null;
      return;
    }
    syncPush();
    void remindUpcoming();
    const timer = window.setInterval(() => {
      void refetchInvites();
      void remindUpcoming();
    }, INVITE_POLL_MS);
    onCleanup(() => window.clearInterval(timer));
  });

  return { user, login, register, logout, refetch, invites, refetchInvites, setInvites, updateMe, changePassword, sessions, revokeSession, revokeOtherSessions };
}

export const auth = createRoot(createAuthStore);
