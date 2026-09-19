import { createSignal, onCleanup, onMount } from "solid-js";

// createFullscreen tracks whether `el()` is the fullscreen element and
// whether the pointer has been idle long enough to hide overlays.
export function createFullscreen(el: () => HTMLElement | undefined, idleAfterMs = 3000) {
  const [active, setActive] = createSignal(false);
  const [idle, setIdle] = createSignal(false);
  let timer: number | null = null;

  const armIdle = () => {
    setIdle(false);
    if (timer !== null) window.clearTimeout(timer);
    timer = window.setTimeout(() => setIdle(true), idleAfterMs);
  };

  const onChange = () => {
    const on = document.fullscreenElement !== null && document.fullscreenElement === el();
    setActive(on);
    if (on) armIdle();
    else {
      setIdle(false);
      if (timer !== null) window.clearTimeout(timer);
    }
  };

  onMount(() => {
    document.addEventListener("fullscreenchange", onChange);
  });
  onCleanup(() => {
    document.removeEventListener("fullscreenchange", onChange);
    if (timer !== null) window.clearTimeout(timer);
  });

  const toggle = () => {
    const target = el();
    if (!target) return;
    if (document.fullscreenElement) void document.exitFullscreen();
    else void target.requestFullscreen();
  };

  // Call on pointer activity inside the stage.
  const touch = () => {
    if (active()) armIdle();
  };

  return { active, idle, toggle, touch };
}

// Overlay panels the viewer can show or hide in fullscreen; the choice
// is remembered per browser.
export type FullscreenPanel = "chat" | "queue";

const prefKey = (panel: FullscreenPanel) => `couchcast.fullscreen.${panel}`;

export function readFullscreenPanel(panel: FullscreenPanel): boolean {
  try {
    return localStorage.getItem(prefKey(panel)) !== "off";
  } catch {
    return true;
  }
}

export function storeFullscreenPanel(panel: FullscreenPanel, on: boolean) {
  try {
    localStorage.setItem(prefKey(panel), on ? "on" : "off");
  } catch {
    // storage unavailable
  }
}
