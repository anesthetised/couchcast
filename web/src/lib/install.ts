import { createSignal } from "solid-js";

// Installation ("add to home screen"): the browser offers it through the
// beforeinstallprompt event, which we hold on to and fire from our own
// button. Nothing shows once the app runs standalone.

type InstallPromptEvent = Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: "accepted" | "dismissed" }> };

const [deferred, setDeferred] = createSignal<InstallPromptEvent | null>(null);

const standalone = () =>
  typeof window !== "undefined" && (window.matchMedia("(display-mode: standalone)").matches || (navigator as { standalone?: boolean }).standalone === true);

if (typeof window !== "undefined") {
  window.addEventListener("beforeinstallprompt", (e) => {
    e.preventDefault();
    if (!standalone()) setDeferred(e as InstallPromptEvent);
  });
  window.addEventListener("appinstalled", () => setDeferred(null));
}

export const installAvailable = () => deferred() !== null;

export async function promptInstall(): Promise<boolean> {
  const ev = deferred();
  if (!ev) return false;
  await ev.prompt();
  const { outcome } = await ev.userChoice;
  setDeferred(null);
  return outcome === "accepted";
}

// registerServiceWorker enables the offline shell and installability in
// production builds; Vite's dev server has no bundle to cache.
export function registerServiceWorker() {
  if (!import.meta.env.PROD || !("serviceWorker" in navigator)) return;
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {});
  });
}
