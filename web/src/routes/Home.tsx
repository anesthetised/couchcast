import { Show, type Component } from "solid-js";

import CreateRoomForm from "~/components/CreateRoomForm";
import InvitesInbox from "~/components/InvitesInbox";
import MyRooms from "~/components/MyRooms";
import { auth } from "~/store/auth";

const Home: Component = () => (
  <Show
    when={auth.user()}
    fallback={
      <section class="card">
        <h1>Watch together</h1>
        <p class="muted">
          <a href="/login">Log in</a> or <a href="/register">register</a> to create a room.
        </p>
      </section>
    }
  >
    <div class="stack">
      <InvitesInbox />
      <MyRooms />
      <CreateRoomForm />
    </div>
  </Show>
);

export default Home;
