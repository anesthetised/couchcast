import { onCleanup } from "solid-js";

// trapFocus keeps Tab inside a modal and closes it on Escape. Call from
// onMount; the listener is removed with the owning component.
export function trapFocus(root: HTMLElement, onClose: () => void) {
  const focusables = () =>
    [...root.querySelectorAll<HTMLElement>("input, select, button, textarea, summary, [tabindex]:not([tabindex='-1'])")].filter(
      (el) => !el.hasAttribute("disabled"),
    );
  focusables()[0]?.focus();
  const onKey = (e: KeyboardEvent) => {
    if (e.key === "Escape") return onClose();
    if (e.key !== "Tab") return;
    const list = focusables();
    const first = list[0];
    const last = list[list.length - 1];
    if (!first || !last) return;
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  };
  document.addEventListener("keydown", onKey);
  onCleanup(() => document.removeEventListener("keydown", onKey));
}
