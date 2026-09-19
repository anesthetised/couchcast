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
            <span class="username">{u().username}</span>
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
