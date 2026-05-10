// PWA service worker (Story 11.10).
//
// Phase 10 ships a minimal app-shell cache so the SPA loads when the
// device is offline. Story 11.10's full Workbox-driven runtime caching
// (offline thumbnails, queued playback events) lands later in Epic 11.
const CACHE = 'mkt-shell-v1';
const SHELL = ['/', '/index.html'];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((c) => c.addAll(SHELL)));
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))),
    ),
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;
  const url = new URL(req.url);
  // Never cache the API or WS endpoints — those want fresh auth.
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws/')) return;
  event.respondWith(
    caches.match(req).then((cached) => cached ?? fetch(req).catch(() => caches.match('/index.html'))),
  );
});
