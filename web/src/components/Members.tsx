import { For, Show, type Component } from "solid-js";

import type { RoomStore } from "~/store/room";

type Props = { room: RoomStore };

const Members: Component<Props> = (props) => {
  const members = () => props.room.state.snapshot?.members ?? [];
  const guests = () => props.room.state.snapshot?.guests ?? 0;

  return (
    <section class="card">
      <h2>
        Watching ({members().length + guests()})
      </h2>
      <ul class="list compact">
        <For each={members()}>
          {(m) => (
            <li class="row">
              <span>
                {m.username}
                <Show when={m.role && m.role !== "member"}>
                  <span class="muted"> · {m.role}</span>
                </Show>
              </span>
              <Show when={m.buffering}>
                <span class="muted small">buffering…</span>
              </Show>
            </li>
          )}
        </For>
        <Show when={guests() > 0}>
          <li class="row muted">
            {guests()} guest{guests() === 1 ? "" : "s"}
          </li>
        </Show>
      </ul>
    </section>
  );
};

export default Members;
