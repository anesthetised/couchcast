import { useNavigate, useParams } from "@solidjs/router";
import { createResource, createSignal, Show, type Component } from "solid-js";

import { join } from "~/lib/rooms";
import { toast } from "~/lib/toast";
import { auth } from "~/store/auth";

// Join: the landing page of an invite link. Shows which room it opens,
// then joins (signed in) or sends the visitor through login first.
const Join: Component = () => {
  const params = useParams<{ token: string }>();
  const navigate = useNavigate();
  const [preview] = createResource(() => params.token, join.preview);
  const [busy, setBusy] = createSignal(false);

  const loginHref = () => `/login?next=${encodeURIComponent(`/join/${params.token}`)}`;

  const accept = async () => {
    setBusy(true);
    try {
      const { roomSlug } = await join.accept(params.token);
      navigate(`/r/${roomSlug}`, { replace: true });
    } catch (err) {
      toast(err instanceof Error ? err.message : String(err), "error");
      setBusy(false);
    }
  };

  const reasonText = (reason?: string) => {
    switch (reason) {
      case "expired":
        return "This invite link has expired.";
      case "revoked":
        return "This invite link was revoked.";
      case "used up":
        return "This invite link has been used up.";
      case "banned":
        return "You are banned from this room.";
      default:
        return "This invite link no longer works.";
    }
  };

  return (
    <div class="join">
      <Show when={!preview.error} fallback={<div class="empty">This invite link is not valid.</div>}>
        <Show when={preview()} fallback={<p class="muted">Loading…</p>}>
          {(p) => (
            <div class="join-card">
              <span class="muted small">You are invited to</span>
              <h1>{p().roomName}</h1>
              <Show
                when={p().valid || p().member}
                fallback={
                  <>
                    <p class="muted">{reasonText(p().reason)}</p>
                    <a class="button ghost" href="/">
                      Back to rooms
                    </a>
                  </>
                }
              >
                <Show
                  when={auth.user()}
                  fallback={
                    <div class="actions">
                      <a class="button" href={loginHref()}>
                        Log in to join
                      </a>
                      <a class="button ghost" href={`/register?next=${encodeURIComponent(`/join/${params.token}`)}`}>
                        Register
                      </a>
                    </div>
                  }
                >
                  <div class="actions">
                    <button type="button" onClick={() => void accept()} disabled={busy()}>
                      {p().member ? "Open room" : "Join room"}
                    </button>
                  </div>
                </Show>
              </Show>
            </div>
          )}
        </Show>
      </Show>
    </div>
  );
};

export default Join;
