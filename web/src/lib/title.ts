import { createEffect, onCleanup } from "solid-js";

const BASE = "couchcast";

// useTitle keeps document.title in step with a reactive string and puts
// the plain name back when the owner unmounts.
export function useTitle(text: () => string) {
  createEffect(() => {
    const t = text();
    document.title = t ? `${t} · ${BASE}` : BASE;
  });
  onCleanup(() => {
    document.title = BASE;
  });
}
