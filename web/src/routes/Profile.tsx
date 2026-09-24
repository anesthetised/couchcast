import { useNavigate } from "@solidjs/router";
import { createEffect, createResource, createSignal, For, Show, type Component } from "solid-js";

import { formatAgo } from "~/lib/format";
import { toast } from "~/lib/toast";
import { describeUA } from "~/lib/ua";
import { AVATAR_COLORS, avatarClass } from "~/lib/types";
import { auth } from "~/store/auth";

// Profile: the account (name, colour), the password and where the user
// is signed in, in the room-settings layout.
const Profile: Component = () => {
  const navigate = useNavigate();
  createEffect(() => {
    if (auth.user.state === "ready" && !auth.user()) navigate("/login?next=/me", { replace: true });
  });

  const [current, setCurrent] = createSignal("");
  const [next, setNext] = createSignal("");
  const [again, setAgain] = createSignal("");
  const [busy, setBusy] = createSignal(false);

  const [sessions, { refetch: reloadSessions }] = createResource(() => auth.user()?.id, () => auth.sessions());
  const signOut = async (id: string) => {
    try {
      await auth.revokeSession(id);
      toast("Signed out.");
    } catch (err) {
      toast(err instanceof Error ? err.message : String(err), "error");
    }
    void reloadSessions();
  };
  const signOutOthers = async () => {
    if (!confirm("Sign out every other device?")) return;
    try {
      const { revoked } = await auth.revokeOtherSessions();
      toast(revoked === 1 ? "Signed out 1 device." : `Signed out ${revoked} devices.`);
    } catch (err) {
      toast(err instanceof Error ? err.message : String(err), "error");
    }
    void reloadSessions();
  };
  const others = () => (sessions() ?? []).filter((s) => !s.current).length;

  const pick = async (color: string) => {
    try {
      await auth.updateMe({ avatarColor: color });
    } catch (err) {
      toast(err instanceof Error ? err.message : String(err), "error");
    }
  };

  const changePassword = async (e: SubmitEvent) => {
    e.preventDefault();
    if (next() !== again()) {
      toast("The new passwords do not match.", "error");
      return;
    }
    setBusy(true);
    try {
      await auth.changePassword(current(), next());
      setCurrent("");
      setNext("");
      setAgain("");
      toast("Password changed. Other devices were signed out.");
      void reloadSessions();
    } catch (err) {
      toast(err instanceof Error ? err.message : String(err), "error");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Show when={auth.user()}>
      {(u) => (
        <div class="settings">
          <header class="room-head">
            <div class="room-title">
              <h1>{u().username}</h1>
              <div class="room-meta">
                <span class="muted small">member since {new Date(u().createdAt).toLocaleDateString()}</span>
                <Show when={u().role === "admin"}>
                  <span class="badge role">admin</span>
                </Show>
              </div>
            </div>
          </header>

          <section class="settings-section">
            <div class="settings-label">
              <h2>Avatar</h2>
              <p class="muted small">The colour behind your initial in chat and presence.</p>
            </div>
            <div class="settings-body">
              <div class="swatches" role="radiogroup" aria-label="Avatar colour">
                <For each={AVATAR_COLORS}>
                  {(c) => (
                    <button
                      type="button"
                      class={`avatar swatch c-${c}`}
                      role="radio"
                      aria-checked={avatarClass(u().username, u().avatarColor) === `c-${c}`}
                      aria-label={c}
                      title={c}
                      onClick={() => void pick(c)}
                    >
                      {u().username.slice(0, 1)}
                    </button>
                  )}
                </For>
              </div>
            </div>
          </section>

          <form class="settings-section" onSubmit={changePassword}>
            <div class="settings-label">
              <h2>Password</h2>
              <p class="muted small">Changing it signs out every other device.</p>
            </div>
            <div class="settings-body form">
              <label>
                Current password
                <input type="password" autocomplete="current-password" required value={current()} onInput={(e) => setCurrent(e.currentTarget.value)} />
              </label>
              <label>
                New password
                <input type="password" autocomplete="new-password" required minLength={8} maxLength={128} value={next()} onInput={(e) => setNext(e.currentTarget.value)} />
              </label>
              <label>
                New password again
                <input type="password" autocomplete="new-password" required value={again()} onInput={(e) => setAgain(e.currentTarget.value)} />
              </label>
              <div class="actions">
                <button type="submit" disabled={busy()}>
                  Change password
                </button>
              </div>
            </div>
          </form>

          <section class="settings-section">
            <div class="settings-label">
              <h2>Sessions</h2>
              <p class="muted small">Where you are signed in. Sign out a device you do not recognise.</p>
            </div>
            <div class="settings-body">
              <table class="table sessions">
                <thead>
                  <tr>
                    <th>Device</th>
                    <th>Last active</th>
                    <th>Signed in</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  <For each={sessions() ?? []}>
                    {(s) => (
                      <tr>
                        <td title={s.userAgent || undefined}>
                          {describeUA(s.userAgent) || "Unknown browser"}
                          <Show when={s.current}>
                            {" "}
                            <span class="badge">this device</span>
                          </Show>
                        </td>
                        <td class="small">{s.current ? "now" : formatAgo(Date.parse(s.lastSeenAt))}</td>
                        <td class="small">{new Date(s.createdAt).toLocaleDateString()}</td>
                        <td class="row-actions">
                          <Show when={!s.current}>
                            <button type="button" class="link danger-text" onClick={() => void signOut(s.id)}>
                              Sign out
                            </button>
                          </Show>
                        </td>
                      </tr>
                    )}
                  </For>
                </tbody>
              </table>
              <Show when={others() > 0}>
                <div class="actions">
                  <button type="button" class="ghost" onClick={() => void signOutOthers()}>
                    Sign out everywhere else
                  </button>
                </div>
              </Show>
            </div>
          </section>
        </div>
      )}
    </Show>
  );
};

export default Profile;
