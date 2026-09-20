import { useNavigate } from "@solidjs/router";
import { Show, type Component } from "solid-js";

import { enabled as notifyOn, notificationsSupported, setEnabled as setNotify } from "~/lib/notify";
import { toast } from "~/lib/toast";
import { auth } from "~/store/auth";

// Header widget: current user with logout, or login/register links.
const UserMenu: Component = () => {
  const navigate = useNavigate();

  const logout = async () => {
    await auth.logout();
    navigate("/", { replace: true });
  };

  const toggleNotify = async () => {
    const on = await setNotify(!notifyOn());
    if (on) toast("Notifications on — invites and your videos starting, while this tab is in the background.");
    else if (Notification.permission === "denied") toast("Notifications are blocked for this site in the browser.", "error");
    else toast("Notifications off.");
  };

  return (
    <nav class="usermenu">
      <Show
        when={auth.user()}
        fallback={
          <>
            <a href="/login">Log in</a>
            <a href="/register" class="button">
              Register
            </a>
          </>
        }
      >
        {(u) => (
          <>
            <Show when={u().role === "admin"}>
              <a href="/admin">Admin</a>
            </Show>
            <Show when={notificationsSupported}>
              <button
                type="button"
                class={`icon-btn bell ${notifyOn() ? "on" : ""}`}
                onClick={() => void toggleNotify()}
                title={notifyOn() ? "Notifications on" : "Notifications off"}
                aria-pressed={notifyOn()}
              >
                <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true">
                  <path d="M6 16V11a6 6 0 0 1 12 0v5l1.5 2h-15z" />
                  <path d="M10 20a2 2 0 0 0 4 0" />
                </svg>
              </button>
            </Show>
            <a class="username" href="/" title={auth.invites().length ? `${auth.invites().length} pending invites` : undefined}>
              <span class="avatar" aria-hidden="true">
                {u().username.slice(0, 1)}
                <Show when={auth.invites().length > 0}>
                  <span class="avatar-badge">{auth.invites().length}</span>
                </Show>
              </span>
              {u().username}
            </a>
            <button type="button" class="link" onClick={logout}>
              Log out
            </button>
          </>
        )}
      </Show>
    </nav>
  );
};

export default UserMenu;
