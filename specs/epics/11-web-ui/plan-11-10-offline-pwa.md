# Implementation Plan — Story 11.10 Offline PWA

> Companion to [story-11-10-offline-pwa.md](story-11-10-offline-pwa.md).
> Idempotency contract per REVIEW §4.4: every replayed POST carries
> `Idempotency-Key`. Server contract owned by Epic 7 Story 7.1.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| SW framework | Workbox 7 with `vite-plugin-pwa`. |
| Placement | `web/src/sw/sw.ts` (Service Worker entry), `web/src/sw/strategies.ts`, `web/src/lib/bgsync/queue.ts`, `web/src/lib/bgsync/replayer.ts`. |
| Storage for queue | IndexedDB via `idb-keyval` (or workbox `BackgroundSyncPlugin` queue store). Queue rows persist `{ id, url, method, headers, body, idempotencyKey, queuedAt }`. |
| Cached endpoints | `/api/libraries`, `/api/videos` (incl. `?library_id=` queries), `/api/videos/{id}`, `/api/search` (results), `/api/search/suggest`, posters/thumbnails. |
| Never cached | `/api/auth/*`, anything with `Authorization: Bearer mkt_pat_*`, video bytes (handled by player). |
| Out of scope | Server-side `Idempotency-Key` (Epic 7 Story 7.1); native app downloads (Story 12.6). |

## 1. Architecture diagram

```
                          ┌──────────────────────────────┐
   navigation requests ──►│  SW: app shell (cache-first) │
                          └──────────────────────────────┘
                          ┌──────────────────────────────┐
   GET /api/libraries  ──►│  SW: stale-while-revalidate  │──► network
                          │      TTL 5 min               │
                          └──────────────────────────────┘
                          ┌──────────────────────────────┐
   POST /api/search/save ►│  online ──► passthrough       │
                          │  offline ─► queue + 202 stub  │
                          └────────────────┬─────────────┘
                                           ▼
                          ┌──────────────────────────────┐
                          │  bgsync replayer (on online) │
                          │  for each row:                │
                          │    fetch(url, { headers: { Idempotency-Key }})│
                          └──────────────────────────────┘
```

## 2. File layout

| Path | Purpose |
|---|---|
| `web/vite.config.ts` | Adds `VitePWA` plugin (injectManifest mode). |
| `web/src/sw/sw.ts` | SW entry; registers strategies + bgsync queue. |
| `web/src/sw/strategies.ts` | Workbox strategies per route. |
| `web/src/lib/bgsync/queue.ts` | Queue model + IndexedDB ops. |
| `web/src/lib/bgsync/replayer.ts` | On `online` event, drain queue. |
| `web/src/lib/bgsync/api.ts` | `enqueueAndStub(req)` — used by API client when offline. |
| `web/src/sw/manifest.webmanifest` | App manifest (see §2.1 for contents). |
| `web/src/features/install/InstallPrompt.tsx` | Triggers `beforeinstallprompt` capture; shown on session 3+. |
| `web/src/features/offline/OfflineBanner.tsx` | Sticky banner when `navigator.onLine === false`. |
| `web/src/features/settings/sections/OfflineQueueSection.tsx` | Lists queued actions; "retry now" / "drop". |
| `web/src/sw/updatePrompt.ts` | "An update is available — Reload" toast wiring. |

### 2.1 `manifest.webmanifest` contents

```json
{
  "name": "Maktaba",
  "short_name": "Maktaba",
  "id": "/",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#…",
  "theme_color": "#…",
  "lang": "ar",
  "dir": "rtl",
  "icons": [
    { "src": "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any maskable" },
    { "src": "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any maskable" }
  ]
}
```

`background_color` and `theme_color` resolve from Story 11.8 design tokens (light theme defaults; the manifest cannot be media-query-aware). `lang`/`dir` ship as Arabic/RTL by default; the in-app language switch (Story 11.12) does not rewrite the manifest at runtime — it only flips `<html lang>` / `<html dir>`. Maskable icons cover Android adaptive icon shapes.

## 3. Workbox strategies

```ts
// strategies.ts
registerRoute(
  ({ request }) => request.mode === 'navigate',
  new NetworkFirst({ cacheName: 'shell-nav', networkTimeoutSeconds: 3 })
);

registerRoute(
  ({ request }) => ['style','script','font'].includes(request.destination),
  new CacheFirst({
    cacheName: 'shell-assets',
    plugins: [new ExpirationPlugin({ maxAgeSeconds: 30 * 24 * 60 * 60 })]
  })
);

registerRoute(
  ({ url }) => /\/api\/(libraries|videos|search|search\/suggest)/.test(url.pathname),
  new StaleWhileRevalidate({
    cacheName: 'api-list',
    plugins: [new ExpirationPlugin({ maxAgeSeconds: 5 * 60 })]
  })
);

registerRoute(
  ({ url }) => /\/posters\/|\/thumbnails\//.test(url.pathname),
  new CacheFirst({
    cacheName: 'images',
    plugins: [new ExpirationPlugin({ maxEntries: 500, maxAgeSeconds: 30 * 24 * 60 * 60 })]
  })
);

// never cache video bytes
registerRoute(
  ({ url }) => url.pathname.startsWith('/stream/'),
  new NetworkOnly()
);
```

## 4. Background sync queue

The API client wraps fetch; on a `POST /api/search/save`, `POST /api/devices/register`, or `POST /api/stream/sessions` while offline:

```ts
async function maybeQueue(req: Request): Promise<Response> {
  if (navigator.onLine) return fetch(req);
  const key = req.headers.get('Idempotency-Key') ?? crypto.randomUUID();
  const cloned = req.clone();
  await queue.put({
    id: key,
    url: req.url, method: req.method,
    headers: Object.fromEntries(cloned.headers),
    body: await cloned.text(),
    idempotencyKey: key,
    queuedAt: Date.now(),
  });
  return new Response(JSON.stringify({ queued: true, key }), {
    status: 202, headers: { 'Content-Type': 'application/json' },
  });
}
```

The replayer drains FIFO on the `online` event:

```ts
window.addEventListener('online', async () => {
  for (const row of await queue.list()) {
    try {
      const res = await fetch(row.url, {
        method: row.method, headers: row.headers, body: row.body,
      });
      if (res.ok || res.status === 409) await queue.delete(row.id); // 409 = already applied
      else if (res.status >= 500) break;                              // stop; retry next online
      else await queue.delete(row.id);                                // 4xx final
    } catch { break; }                                                // network blip
  }
});
```

Whitelist: only the three POSTs above plus `POST /api/me/playback-state`. Never queue auth requests; never queue `DELETE`. Per the story EC, `/progress` is naturally idempotent and has no `Idempotency-Key`; we just drop the offline call.

## 5. Update flow

`vite-plugin-pwa` exposes `useRegisterSW({ onNeedRefresh, onOfflineReady })`. On `onNeedRefresh`, show a toast with a "Reload" button → `updateSW(true)`.

```ts
const { updateServiceWorker } = useRegisterSW({
  onNeedRefresh() { toast.show('An update is available', { actionLabel: 'Reload', onAction: () => updateServiceWorker(true) }); },
  onOfflineReady() { /* silent */ },
});
```

## 6. Install prompt

- Capture `beforeinstallprompt` once.
- Increment `localStorage.maktaba.session_count` per fresh tab.
- On `session_count >= 3` and not previously dismissed, render `<InstallPrompt>` with "Install Maktaba" + "Maybe later".

## 7. Offline UI

`<OfflineBanner>` reads `navigator.onLine` + `online`/`offline` events. Settings → Offline queue lists `await queue.list()` with per-row "Retry now" (force a single drain) and "Drop".

## 8. Edge cases

| Case | Handling |
|---|---|
| Quota exhaustion (Safari ITP) | LRU trim path in §10 keeps Safari usage ≤ 45 MB. If a `quotaexceeded` still escapes, the SW logs once and the `<OfflineBanner>` reads `navigator.storage.estimate()` and surfaces "Offline cache full". |
| iOS Safari kills SW after 30 s | We never depend on long-lived workers; replay is event-driven on `online`. |
| Auth POST queued? | Never — see whitelist. |
| Server idempotency window expired | Replay creates a new row; tolerated for `save-search` / `register-device`. |
| Stream session POST replays after expiry | Server returns the original session if within window; otherwise mints a new one — both acceptable. |

## 9. Test cases

### 9.1 Unit

| Test | Asserts |
|---|---|
| `maybeQueue stubs and persists when offline` | Sets navigator.onLine = false; receives 202; queue row present. |
| `replayer drains FIFO` | 3 queued items; `online` fires; mock fetch called in order. |
| `auth POST not queued` | `/api/auth/login` returns rejected promise when offline. |
| `409 on replay deletes row` | Conflict treated as "already applied". |

### 9.2 e2e

| Test | Asserts |
|---|---|
| `offline + visited library renders from cache` | Workbox cache populated; offline → grid still renders. |
| `replay duplicate returns same id` | Mock backend echoes idempotent response; queue empty after one drain. |
| `SW update toast` | Bump build hash; stale tab shows toast; reload fetches new bundle. |
| `install prompt at session 3` | Bump session count; prompt appears. |

## 10. Performance & cache budget

- SW registration deferred to `requestIdleCallback` after first paint.
- Cached responses purged via `ExpirationPlugin` (LRU).
- **Cache budget — Safari ITP-aware.** A nominal budget of 50 MB images + 5 MB API + 10 MB shell ≈ 65 MB total can blow Safari's ITP storage cap (which can clip to ~50 MB for non-installed origins, and evicts entire origins under pressure). Hard cap on Safari: **≤ 45 MB total**. On Chromium/Firefox the soft target stays at 65 MB.
- **LRU trim path keyed on `navigator.storage.estimate()`.** Every N writes (default `N = 25`), the SW reads `navigator.storage.estimate()` and, if `usage > 45 MB` (Safari) or `> 60 MB` (other UAs), evicts the oldest images cache entries until under the threshold. Skeleton:

```ts
// web/src/sw/cacheBudget.ts
const SAFARI_HARD_CAP = 45 * 1024 * 1024;  // 45 MB
const OTHER_SOFT_CAP  = 60 * 1024 * 1024;  // 60 MB
const CHECK_EVERY     = 25;                 // writes between checks
let writeCounter = 0;

export async function maybeTrim() {
  if (++writeCounter % CHECK_EVERY !== 0) return;
  if (!navigator.storage?.estimate) return;
  const { usage = 0 } = await navigator.storage.estimate();
  const cap = isSafari() ? SAFARI_HARD_CAP : OTHER_SOFT_CAP;
  if (usage <= cap) return;

  const cache = await caches.open('images');
  const reqs = await cache.keys();                  // FIFO order ≈ LRU under Workbox ExpirationPlugin
  for (const req of reqs) {
    await cache.delete(req);
    const { usage: now = 0 } = await navigator.storage.estimate();
    if (now <= cap * 0.85) break;                   // trim to 85% of cap to avoid thrash
  }
}
```

`maybeTrim()` is called after every `cache.put` in the `images` and `api-list` handlers (wrapped via a Workbox plugin's `cacheDidUpdate`).

## 11. Dependencies

- Server `Idempotency-Key` honored by Epic 7 Story 7.1.
- `useFeatureFlag('admin')` for admin CTAs (none directly here).
- `lib/api/*` clients route through `maybeQueue`.
