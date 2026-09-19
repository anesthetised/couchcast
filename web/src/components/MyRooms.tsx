import { createResource, For, Show, type Component } from "solid-js";

import { rooms } from "~/lib/rooms";

const MyRooms: Component = () => {
  const [list] = createResource(rooms.mine);

  return (
    <section class="card">
      <h2>Your rooms</h2>
      <Show when={list()} fallback={<p class="muted">Loading…</p>}>
        {(items) => (
          <Show when={items().length > 0} fallback={<p class="muted">No rooms yet.</p>}>
            <ul class="list">
              <For each={items()}>
                {(room) => (
                  <li class="row">
                    <a href={`/r/${room.slug}`}>
                      <strong>{room.name}</strong> <span class="muted">/r/{room.slug}</span>
                    </a>
                    <span class="muted">
                      {room.visibility} · {room.myRole}
                    </span>
                  </li>
                )}
              </For>
            </ul>
          </Show>
        )}
      </Show>
    </section>
  );
};

export default MyRooms;
