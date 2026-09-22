// couchcast service worker: enough for installability and a shell that
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
