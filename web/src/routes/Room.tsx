import { useParams } from "@solidjs/router";
import { createResource, For, Show, type Component } from "solid-js";

import { ApiError } from "~/lib/api";
import { rooms } from "~/lib/rooms";
import { isModerator } from "~/lib/types";
import { auth } from "~/store/auth";

// Room page. The player, queue and chat arrive in later phases; for now it
// shows the room header and members so access rules can be exercised.
const Room: Component = () => {
  const params = useParams<{ slug: string }>();
  const [room] = createResource(() => params.slug, rooms.get);
  const [members] = createResource(
    () => (room() ? params.slug : undefined),
    (slug) => rooms.members(slug),
  );

  const errorMessage = () => {
    const err = room.error as unknown;
    if (err instanceof ApiError) {
      if (err.status === 401) return "Log in to view this room.";
      return err.message;
    }
    return err ? String(err) : null;
  };

  return (
    <Show when={!room.error} fallback={<section class="card error">{errorMessage()}</section>}>
      <Show when={room()} fallback={<p class="muted">Loading…</p>}>
        {(r) => (
          <div class="stack">
            <header class="card room-header">
              <div>
                <h1>{r().name}</h1>
                <p class="muted">
                  /r/{r().slug} · {r().visibility} · owner {r().owner}
                  <Show when={r().myRole}> · you are {r().myRole}</Show>
                </p>
              </div>
              <Show when={isModerator(r().myRole)}>
                <a class="button" href={`/r/${r().slug}/settings`}>
                  Settings
                </a>
              </Show>
            </header>

            <section class="card player-placeholder">
              <p class="muted">Player, queue and chat land in the next phases.</p>
              <Show when={!auth.user()}>
                <p class="muted">
                  <a href="/login">Log in</a> to chat and vote.
                </p>
              </Show>
            </section>

            <section class="card">
              <h2>Members ({r().memberCount})</h2>
              <ul class="list">
                <For each={members() ?? []}>
                  {(m) => (
                    <li class="row">
                      <span>{m.username}</span>
                      <span class="muted">{m.role}</span>
                    </li>
                  )}
                </For>
              </ul>
            </section>
          </div>
        )}
      </Show>
    </Show>
  );
};

export default Room;
