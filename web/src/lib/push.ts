import { api, ApiError } from "~/lib/api";

// Web Push: the service worker receives notifications while no tab is
// open. It rides on the bell toggle (lib/notify.ts): turning it on
// subscribes this browser, turning it off unsubscribes. The server may
// have push switched off (no VAPID key) — then only in-tab notifications
// work, as before.

const supported = () => typeof navigator !== "undefined" && "serviceWorker" in navigator && "PushManager" in window;

let serverKey: Promise<string | null> | null = null;

function publicKey(): Promise<string | null> {
  serverKey ??= api<{ publicKey: string }>("/api/v1/push/key")
    .then((r) => r.publicKey || null)
    .catch((err: unknown) => {
      serverKey = null; // retry next time; a 404 means push is off server-side
      if (err instanceof ApiError && err.status === 404) return null;
      throw err;
    });
  return serverKey;
}

function toBytes(b64url: string): Uint8Array<ArrayBuffer> {
  const b64 = b64url.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((b64url.length + 3) % 4);
  const raw = atob(b64);
  const out = new Uint8Array(raw.length);
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

async function registration(): Promise<ServiceWorkerRegistration | null> {
  if (!supported()) return null;
  try {
    return await navigator.serviceWorker.ready;
  } catch {
    return null;
  }
}

// subscribePush makes sure this browser's subscription is stored for the
// signed-in user; true when push is active.
export async function subscribePush(): Promise<boolean> {
  const key = await publicKey().catch(() => null);
  const reg = key ? await registration() : null;
  if (!key || !reg) return false;
  try {
    const sub =
      (await reg.pushManager.getSubscription()) ??
      (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: toBytes(key) }));
    const json = sub.toJSON();
    await api<void>("/api/v1/push/subscriptions", {
      method: "POST",
      body: JSON.stringify({ endpoint: json.endpoint, keys: json.keys }),
    });
    return true;
  } catch {
    return false;
  }
}

// unsubscribePush forgets this browser on the server and at the push
// service.
export async function unsubscribePush(): Promise<void> {
  const reg = await registration();
  const sub = await reg?.pushManager.getSubscription();
  if (!sub) return;
  await api<void>("/api/v1/push/subscriptions", { method: "DELETE", body: JSON.stringify({ endpoint: sub.endpoint }) }).catch(() => {});
  await sub.unsubscribe().catch(() => false);
}
