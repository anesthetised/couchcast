import { Show, type Component } from "solid-js";

import { auth } from "~/store/auth";

// Landing page. Phase 3 replaces the body with room creation, the user's
// rooms and pending invites.
const Home: Component = () => (
  <section class="card">
    <h1>Watch together</h1>
    <Show
      when={auth.user()}
      fallback={
        <p class="muted">
          <a href="/login">Log in</a> or <a href="/register">register</a> to create a room.
        </p>
      }
    >
      {(u) => <p class="muted">Signed in as {u().username}. Rooms arrive in the next phase.</p>}
    </Show>
  </section>
);

export default Home;
