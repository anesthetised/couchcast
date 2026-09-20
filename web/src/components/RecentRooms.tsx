import { For, Show, type Component } from "solid-js";

import { forgetVisit, recent } from "~/lib/recent";

// RecentRooms is a quiet row of the last rooms this browser opened.
const RecentRooms: Component = () => (
  <Show when={recent().length > 0}>
    <section class="recent" aria-label="Recently visited">
      <span class="recent-label">Recent</span>
      <For each={recent()}>
        {(r) => (
          <span class="recent-item">
            <a href={`/r/${r.slug}`}>{r.name}</a>
            <button type="button" class="chip-x" aria-label={`Forget ${r.name}`} title="Remove" onClick={() => forgetVisit(r.slug)}>
              ×
            </button>
          </span>
        )}
      </For>
    </section>
  </Show>
);

export default RecentRooms;
