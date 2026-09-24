// couchcast service worker: Web Push, installability and a shell that
// opens offline. Hashed assets are cached first (they never change under
// the same name); navigations go to the network and fall back to the
// last shell we saw; the API, media and the socket are never touched.
const SHELL = "couchcast-shell-v1";
const ASSETS = "couchcast-assets-v1";

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(SHELL)
      .then((cache) => cache.add("/"))
      .catch(() => {}),
  );
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) => Promise.all(keys.filter((k) => k !== SHELL && k !== ASSETS).map((k) => caches.delete(k)))),
  );
  self.clients.claim();
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  if (req.mode === "navigate") {
    event.respondWith(
      fetch(req)
        .then((res) => {
          if (res.ok && (res.headers.get("content-type") || "").includes("text/html")) {
            const copy = res.clone();
            caches.open(SHELL).then((cache) => cache.put("/", copy)).catch(() => {});
          }
          return res;
        })
        .catch(() => caches.match("/").then((hit) => hit || Response.error())),
    );
    return;
  }

  if (url.pathname.startsWith("/assets/") || url.pathname.startsWith("/icons/")) {
    event.respondWith(
      caches.match(req).then(
        (hit) =>
          hit ||
          fetch(req).then((res) => {
            if (res.ok) {
              const copy = res.clone();
              caches.open(ASSETS).then((cache) => cache.put(req, copy)).catch(() => {});
            }
            return res;
          }),
      ),
    );
  }
});

// Push: the server sends {title, body, url, tag}. A notification with the
// same tag replaces the earlier one, so the page's own in-tab
// notification and the push never both show.
self.addEventListener("push", (event) => {
  let msg = {};
  try {
    msg = event.data ? event.data.json() : {};
  } catch {
    msg = { title: "couchcast", body: event.data ? event.data.text() : "" };
  }
  event.waitUntil(
    self.registration.showNotification(msg.title || "couchcast", {
      body: msg.body || "",
      tag: msg.tag || undefined,
      icon: "/icons/icon-192.png",
      badge: "/icons/icon-192.png",
      data: { url: msg.url || "/" },
    }),
  );
});

// A click focuses a tab already on that page, or opens one.
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = new URL((event.notification.data && event.notification.data.url) || "/", self.location.origin).href;
  event.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((wins) => {
      for (const w of wins) {
        if (w.url === url && "focus" in w) return w.focus();
      }
      return self.clients.openWindow(url);
    }),
  );
});
