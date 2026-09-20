import { createSignal } from "solid-js";

// Browser notifications are opt-in and only fire while the tab is hidden:
// an open tab already shows the event.

const KEY = "couchcast.notifications";

const supported = typeof Notification !== "undefined";
const [enabled, setEnabledSignal] = createSignal(supported && read() && Notification.permission === "granted");

function read(): boolean {
  try {
    return localStorage.getItem(KEY) === "on";
  } catch {
    return false;
  }
}

function write(on: boolean) {
  try {
    localStorage.setItem(KEY, on ? "on" : "off");
  } catch {
    // storage unavailable
  }
}

// setEnabled asks for permission on the way in; it resolves to the final
// state so the caller can reflect a denied request.
export async function setEnabled(on: boolean): Promise<boolean> {
  if (!supported) return false;
  if (on && Notification.permission !== "granted") {
    const p = await Notification.requestPermission();
    if (p !== "granted") {
      write(false);
      setEnabledSignal(false);
      return false;
    }
  }
  write(on);
  setEnabledSignal(on);
  return on;
}

export function notify(title: string, body: string, tag: string) {
  if (!enabled() || document.visibilityState === "visible") return;
  try {
    const n = new Notification(title, { body, tag });
    n.onclick = () => {
      window.focus();
      n.close();
    };
  } catch {
    // some platforms throw from the constructor (e.g. Android Chrome)
  }
}

export { enabled, supported as notificationsSupported };
