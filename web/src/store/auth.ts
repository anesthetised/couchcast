import { createResource, createRoot } from "solid-js";

import { api, ApiError } from "~/lib/api";
import type { Invite, User } from "~/lib/types";

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

  return { user, login, register, logout, refetch, invites, refetchInvites, setInvites };
}

export const auth = createRoot(createAuthStore);
