// PWA service worker (Story 11.10).
//
//   - App shell (page loads, /, /index.html, /theme-boot.js): NETWORK-FIRST,
//     the cache only when offline. It was cache-first under a build stamp
//     that never changed, so the first shell a browser saw was kept for
//     ever: after an update it named the old bundle's script, the server
//     answered that missing script with the page, and the app (the Mac
//     app's window above all) stayed blank.
//   - /api/*: NETWORK-ONLY. The API authenticates via the HttpOnly
//     `mkt_sess` cookie (web/src/lib/api.ts sends credentials:"include").
//     Cache Storage is keyed by URL only — it cannot tell user A's
//     `/api/videos` from user B's. There is NO offline requirement in
//     scope, so we never read or write the SWR cache for any `/api/*`
//     request. This is a security boundary: do NOT add an `/api/*`
//     allowlist back without a per-user cache key (default-deny).
//   - Mutations (non-GET) and WS: pass through untouched.
//
// A new SW takes over at once (skipWaiting on install): a waiting SW
// behind a stale shell could never be accepted, since that shell cannot
// render the in-app update toast. Taking over cannot resurrect a legacy
// poisoned API cache: activate deletes every cache but this shell's
// before it claims the pages, and this SW never reads an API cache.
const BUILD = "v3";
const SHELL_CACHE = "mkt-shell-" + BUILD;
// theme-boot.js runs before first paint (no theme flash), so it is shell too.
const SHELL = ["/", "/index.html", "/theme-boot.js"];

// Any cache name matching this is purged on activate and on logout.
// Covers the legacy `mkt-api-v1` / `mkt-api-v2` SWR caches that older
// SW versions populated with cross-user authenticated bodies.
function isApiCache(name) {
  return name.startsWith("mkt-api-");
}

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(SHELL_CACHE).then((c) => c.addAll(SHELL)));
  // takes over at once — see header comment
  self.skipWaiting();
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

  // Live TV (HLS playlists/segments, the XMLTV guide, the M3U lineup) is
  // live data proxied to the streaming service: straight to the network,
  // never the SPA fallback.
  if (url.pathname.startsWith("/stream/")) return;
  if (url.pathname === "/xmltv.xml" || url.pathname === "/lineup.m3u") return;

  // /api/* is network-only: do not consult or populate any cache.
  if (url.pathname.startsWith("/api/")) return;

  // App shell: network-first (the current build's page), kept for offline.
  if (req.mode === "navigate" || SHELL.includes(url.pathname)) {
    event.respondWith(
      fetch(req)
        .then((res) => {
          if (res.ok) {
            const copy = res.clone();
            const key = req.mode === "navigate" ? "/index.html" : req;
            event.waitUntil(caches.open(SHELL_CACHE).then((c) => c.put(key, copy)));
          }
          return res;
        })
        .catch(() => caches.match(req).then((cached) => cached || caches.match("/index.html")))
    );
  }
  // Everything else (the hashed bundle, art, icons): the network.
});
