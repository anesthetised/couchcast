import { useNavigate } from "@solidjs/router";
import { createEffect, createSignal, For, Show, type Component } from "solid-js";

import { toast } from "~/lib/toast";
import { AVATAR_COLORS, avatarClass } from "~/lib/types";
import { auth } from "~/store/auth";

// Profile: the account (name, colour) and the password, in the
// room-settings layout.
const Profile: Component = () => {
  const navigate = useNavigate();
  createEffect(() => {
    if (auth.user.state === "ready" && !auth.user()) navigate("/login?next=/me", { replace: true });
  });

  const [current, setCurrent] = createSignal("");
  const [next, setNext] = createSignal("");
  const [again, setAgain] = createSignal("");
  const [busy, setBusy] = createSignal(false);

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
        </div>
      )}
    </Show>
  );
};

export default Profile;
