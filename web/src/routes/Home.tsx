import { Show, type Component } from "solid-js";

import CreateRoomForm from "~/components/CreateRoomForm";
import InvitesInbox from "~/components/InvitesInbox";
import MyRooms from "~/components/MyRooms";
import PublicRooms from "~/components/PublicRooms";
import { auth } from "~/store/auth";

const Home: Component = () => (
  <div class="stack">
    <PublicRooms />
    <Show
      when={auth.user()}
      fallback={
        <section class="card">
          <p class="muted">
            <a href="/login">Log in</a> or <a href="/register">register</a> to create a room.
          </p>
        </section>
      }
    >
      <InvitesInbox />
      <MyRooms />
      <CreateRoomForm />
    </Show>
  </div>
);

export default Home;
