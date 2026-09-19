import { createSignal, Show, type Component } from "solid-js";

import CreateRoomForm from "~/components/CreateRoomForm";
import Directory from "~/components/Directory";
import InvitesInbox from "~/components/InvitesInbox";
import { auth } from "~/store/auth";

// Home: a hero with the create toggle, pending invites, then the room
// directory for everyone.
const Home: Component = () => {
  const [creating, setCreating] = createSignal(false);

  return (
    <div class="stack">
      <header class="hero">
        <div>
          <h1>Watch together</h1>
          <p>Queue a video, share the link, stay in sync.</p>
        </div>
        <div class="actions">
          <Show
            when={auth.user()}
            fallback={
              <>
                <a class="button ghost" href="/login">
                  Log in
                </a>
                <a class="button" href="/register">
                  Register
                </a>
              </>
            }
          >
            <button type="button" aria-expanded={creating()} onClick={() => setCreating(!creating())}>
              {creating() ? "Close" : "+ New room"}
            </button>
          </Show>
        </div>
      </header>

      <Show when={auth.user()}>
        <div class="collapse" data-open={creating()}>
          <div>
            <Show when={creating()}>
              <CreateRoomForm onCancel={() => setCreating(false)} />
            </Show>
          </div>
        </div>
        <InvitesInbox />
      </Show>

      <Directory />
    </div>
  );
};

export default Home;
