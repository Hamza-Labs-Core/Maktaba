// PWA service worker (Story 11.10).
//
//   - App shell: cache-first. The cache name carries a build stamp so a
//     new deploy busts the old shell (the registration in src/lib/pwa.ts
//     fires `mkt:sw-update` so the UI can offer a reload).
//   - /api/*: NETWORK-ONLY. The API authenticates via the HttpOnly
//     `mkt_sess` cookie (web/src/lib/api.ts sends credentials:"include").
//     Cache Storage is keyed by URL only — it cannot tell user A's
//     `/api/videos` from user B's. There is NO offline requirement in
//     scope, so we never read or write the SWR cache for any `/api/*`
//     request. This is a security boundary: do NOT add an `/api/*`
//     allowlist back without a per-user cache key (default-deny).
//   - Mutations (non-GET) and WS: pass through untouched.
//
// `skipWaiting()` is NOT called on install: a new SW stays "waiting"
// until the user accepts the in-app update (pwa.ts posts SKIP_WAITING
// on the reload action). This guarantees a mid-session SW swap cannot
// silently activate and resurrect any legacy poisoned API cache while
// the page is still showing the previous user's data.
const BUILD = "v2";
const SHELL_CACHE = "mkt-shell-" + BUILD;
const SHELL = ["/", "/index.html"];

// Any cache name matching this is purged on activate and on logout.
// Covers the legacy `mkt-api-v1` / `mkt-api-v2` SWR caches that older
// SW versions populated with cross-user authenticated bodies.
function isApiCache(name) {
  return name.startsWith("mkt-api-");
}

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(SHELL_CACHE).then((c) => c.addAll(SHELL)));
  // Intentionally NO self.skipWaiting() — see header comment.
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      // Keep only the current shell cache. This deletes every legacy
      // API cache (mkt-api-*) and any stale shell, so the first time
      // this SW activates it scrubs the poisoned cross-user store.
      .then((keys) =>
        Promise.all(keys.filter((k) => k !== SHELL_CACHE).map((k) => caches.delete(k)))
      )
      .then(() => self.clients.claim())
  );
});

self.addEventListener("message", (event) => {
  const data = event.data;
  if (!data || typeof data.type !== "string") return;
  if (data.type === "SKIP_WAITING") {
    // Driven by the in-app "reload to update" affordance (pwa.ts).
    self.skipWaiting();
    return;
  }
  if (data.type === "PURGE_API_CACHE") {
    // Driven by the client logout path (src/lib/sw.ts). Defensive:
    // this SW never writes an API cache, but an older SW version may
    // have left one behind before this build activated.
    event.waitUntil(
      caches
        .keys()
        .then((keys) => Promise.all(keys.filter((k) => isApiCache(k)).map((k) => caches.delete(k))))
    );
  }
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);

  if (url.pathname.startsWith("/ws/")) return;

  // /api/* is network-only: do not consult or populate any cache.
  if (url.pathname.startsWith("/api/")) return;

  // App shell: cache-first with a network fallback to the SPA entry.
  event.respondWith(
    caches
      .match(req)
      .then((cached) => cached || fetch(req).catch(() => caches.match("/index.html")))
  );
});
