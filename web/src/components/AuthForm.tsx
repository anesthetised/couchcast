import { useNavigate, useSearchParams } from "@solidjs/router";
import { createSignal, Show, type Component } from "solid-js";

import { auth, type Credentials } from "~/store/auth";

type Props = {
  mode: "login" | "register";
};

// Shared login/register form; the only differences are the label and which
// store action is called.
const AuthForm: Component<Props> = (props) => {
  const navigate = useNavigate();
  const [params] = useSearchParams<{ next?: string }>();
  // Only same-origin paths are honoured, never absolute URLs.
  const next = () => (params.next?.startsWith("/") && !params.next.startsWith("//") ? params.next : "/");
  const [username, setUsername] = createSignal("");
  const [password, setPassword] = createSignal("");
  const [error, setError] = createSignal<string | null>(null);
  const [busy, setBusy] = createSignal(false);

  const submit = async (e: SubmitEvent) => {
    e.preventDefault();
    setError(null);
    setBusy(true);
    const c: Credentials = { username: username().trim(), password: password() };
    try {
      await (props.mode === "login" ? auth.login(c) : auth.register(c));
      navigate(next(), { replace: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form class="card form auth-form" onSubmit={submit}>
      <h1>{props.mode === "login" ? "Log in" : "Create account"}</h1>

      <label>
        Username
        <input
          type="text"
          autocomplete="username"
          required
          minLength={3}
          maxLength={32}
          pattern="[A-Za-z0-9_]{3,32}"
          value={username()}
          onInput={(e) => setUsername(e.currentTarget.value)}
        />
      </label>

      <label>
        Password
        <input
          type="password"
          autocomplete={props.mode === "login" ? "current-password" : "new-password"}
          required
          minLength={8}
          maxLength={128}
          value={password()}
          onInput={(e) => setPassword(e.currentTarget.value)}
        />
      </label>

      <Show when={error()}>{(msg) => <p class="error">{msg()}</p>}</Show>

      <button type="submit" disabled={busy()}>
        {props.mode === "login" ? "Log in" : "Register"}
      </button>

      <p class="muted">
        {props.mode === "login" ? (
          <>
            No account? <a href={`/register${params.next ? `?next=${encodeURIComponent(params.next)}` : ""}`}>Register</a>
          </>
        ) : (
          <>
            Already registered? <a href={`/login${params.next ? `?next=${encodeURIComponent(params.next)}` : ""}`}>Log in</a>
          </>
        )}
      </p>
    </form>
  );
};

export default AuthForm;
