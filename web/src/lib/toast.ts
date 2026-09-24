import { createSignal } from "solid-js";

import { logEvent } from "~/lib/diagnostics";

export type Toast = { id: number; kind: "info" | "error"; text: string };

const [toasts, setToasts] = createSignal<Toast[]>([]);
let seq = 0;

// toast shows a short message in the corner; errors linger a bit longer.
export function toast(text: string, kind: Toast["kind"] = "info") {
  if (kind === "error") logEvent("toast", text);
  const id = ++seq;
  setToasts((t) => [...t.slice(-3), { id, kind, text }]);
  window.setTimeout(() => dismiss(id), kind === "error" ? 6000 : 3000);
}

export function dismiss(id: number) {
  setToasts((t) => t.filter((x) => x.id !== id));
}

export { toasts };
