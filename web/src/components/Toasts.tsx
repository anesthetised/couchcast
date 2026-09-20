import { For, type Component } from "solid-js";

import { dismiss, toasts } from "~/lib/toast";

const Toasts: Component = () => (
  <div class="toasts" aria-live="polite">
    <For each={toasts()}>
      {(t) => (
        <button type="button" class={`toast ${t.kind}`} onClick={() => dismiss(t.id)}>
          {t.text}
        </button>
      )}
    </For>
  </div>
);

export default Toasts;
