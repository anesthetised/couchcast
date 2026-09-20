import { Show, type Component } from "solid-js";

import Directory from "~/components/Directory";
import InvitesInbox from "~/components/InvitesInbox";
import { auth } from "~/store/auth";

// Home: a hero with the create link, pending invites, then the room
// directory for everyone.
const Home: Component = () => {
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
            <a class="button" href="/new">
              + New room
            </a>
          </Show>
        </div>
      </header>

      <Show when={auth.user()}>
        <InvitesInbox />
      </Show>

      <Directory />
    </div>
  );
};

export default Home;
