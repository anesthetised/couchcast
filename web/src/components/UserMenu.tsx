import { useNavigate } from "@solidjs/router";
import { Show, type Component } from "solid-js";

import { auth } from "~/store/auth";

// Header widget: current user with logout, or login/register links.
const UserMenu: Component = () => {
  const navigate = useNavigate();

  const logout = async () => {
    await auth.logout();
    navigate("/", { replace: true });
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
