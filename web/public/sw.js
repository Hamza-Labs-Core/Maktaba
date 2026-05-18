// PWA service worker (Story 11.10).
//
//   - App shell: cache-first. The cache name carries a build stamp so a
//     new deploy busts the old shell (the registration in src/lib/pwa.ts
//     fires `mkt:sw-update` so the UI can offer a reload).
//   - GET /api/* metadata: stale-while-revalidate with a 5-minute TTL —
//     the cached copy is served instantly, then refreshed in the
//     background; entries older than the TTL are re-fetched inline.
//   - Auth/stream/WS endpoints: never cached (always fresh credentials).
//   - Mutations (non-GET): pass through untouched.
const BUILD = "v2";
const SHELL_CACHE = "mkt-shell-" + BUILD;
const API_CACHE = "mkt-api-" + BUILD;
const SHELL = ["/", "/index.html"];
const API_TTL_MS = 5 * 60 * 1000;

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(SHELL_CACHE).then((c) => c.addAll(SHELL)));
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  const keep = new Set([SHELL_CACHE, API_CACHE]);
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => !keep.has(k)).map((k) => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

function isFreshMetadata(req) {
  const url = new URL(req.url);
  if (!url.pathname.startsWith("/api/")) return false;
  // Only cache safe, idempotent metadata reads — never auth/stream.
  if (url.pathname.startsWith("/api/auth/") || url.pathname.startsWith("/api/stream/")) {
    return false;
  }
  return true;
}

async function staleWhileRevalidate(req) {
  const cache = await caches.open(API_CACHE);
  const cached = await cache.match(req);
  const fetchAndStore = fetch(req)
    .then((res) => {
      if (res.ok) {
        const clone = res.clone();
        const headers = new Headers(clone.headers);
        headers.set("x-mkt-cached-at", String(Date.now()));
        clone.blob().then((body) => {
          cache.put(
            req,
            new Response(body, {
              status: clone.status,
              statusText: clone.statusText,
              headers,
            })
          );
        });
      }
      return res;
    })
    .catch(() => cached);

  if (cached) {
    const at = Number(cached.headers.get("x-mkt-cached-at") || 0);
    if (Date.now() - at < API_TTL_MS) return cached; // fresh: serve, revalidate in bg
    return fetchAndStore; // stale: wait for the network refresh
  }
  return fetchAndStore;
}

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);

  if (url.pathname.startsWith("/ws/")) return;

  if (url.pathname.startsWith("/api/")) {
    if (isFreshMetadata(req)) {
      event.respondWith(staleWhileRevalidate(req));
    }
    return;
  }

  event.respondWith(
    caches
      .match(req)
      .then((cached) => cached || fetch(req).catch(() => caches.match("/index.html")))
  );
});
