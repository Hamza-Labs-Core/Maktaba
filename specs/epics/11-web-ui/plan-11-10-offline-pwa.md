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
| Cached endpoints | `/api/libraries`, `/api/libraries/{id}/videos`, `/api/videos/{id}`, `/api/search` (results), `/api/search/suggest`, posters/thumbnails. |
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
| `web/src/sw/manifest.webmanifest` | App manifest. |
| `web/src/features/install/InstallPrompt.tsx` | Triggers `beforeinstallprompt` capture; shown on session 3+. |
| `web/src/features/offline/OfflineBanner.tsx` | Sticky banner when `navigator.onLine === false`. |
| `web/src/features/settings/sections/OfflineQueueSection.tsx` | Lists queued actions; "retry now" / "drop". |
| `web/src/sw/updatePrompt.ts` | "An update is available — Reload" toast wiring. |

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
| Quota exhaustion (Safari ITP) | `quotaexceeded` from cache.put → SW logs once; `<OfflineBanner>` reads `navigator.storage.estimate()` and surfaces "Offline cache full". |
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

## 10. Performance

- SW registration deferred to `requestIdleCallback` after first paint.
- Cached responses purged via `ExpirationPlugin` (LRU).
- Cache budget: 50 MB images + 5 MB API + 10 MB shell — totals ≤ 65 MB (well under Safari ITP cap).

## 11. Dependencies

- Server `Idempotency-Key` honored by Epic 7 Story 7.1.
- `useFeatureFlag('admin')` for admin CTAs (none directly here).
- `lib/api/*` clients route through `maybeQueue`.
