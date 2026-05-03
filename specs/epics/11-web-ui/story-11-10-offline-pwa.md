# Story 11.10 — Offline capability (PWA service worker)

A Workbox-driven service worker caches the app shell and recently-fetched
metadata; offline, the user can browse what they've already seen and
queue actions for later sync.

**Anchors:** [`architecture.md` §6.2](../../architecture.md). Depends on
Epic 7 Story 7.1 (HTTP skeleton + Idempotency-Key — see below).

## Idempotency contract (resolves [REVIEW §4.4](../../REVIEW.md))

Every queued POST replayed by `bgsync` MUST carry an `Idempotency-Key`
header (UUIDv4 generated client-side at queue time and persisted with
the queue entry). The server-side contract for `Idempotency-Key` is owned
by Epic 7 Story 7.1; this story consumes it for queued retries:

- `POST /api/stream/sessions` — replay returns the original session
  rather than minting a duplicate.
- `POST /api/search/save` — replay returns the original saved-search row.
- `POST /api/devices/register` — replay updates the existing token.
- `POST /api/stream/sessions/{id}/progress` — natural last-writer-wins;
  no idempotency required (already idempotent).

The service worker never queues `POST /api/auth/login`,
`POST /api/auth/refresh`, or any DELETE; those are too sensitive or
state-dependent for transparent replay.

## AC

- Service worker is registered after first paint (no blocking).
- App shell (HTML, JS, CSS, fonts) cached with `cache-first`, max age 30
  days, busted by a build hash.
- Library list, video metadata, search results: `stale-while-revalidate`,
  TTL 5 min.
- Video bytes: never cached by the SW; the player handles its own buffer.
- Offline UI: a banner "You are offline — showing cached results"; queued
  actions list visible in Settings → Offline queue with manual
  "retry now" / "drop" affordances.
- "Install Maktaba" prompt is shown once at session 3+, dismissable.
- Update flow: on a new SW version, show "An update is available — Reload"
  toast.

## TC

- Offline + previously-visited library: the grid renders from cache;
  poster images appear from cache.
- Offline + never-visited library: shell renders, content area shows
  offline empty state.
- Replay queue: queue 3 "save search" actions offline → reconnect → all 3
  fire in order; if one returns 409, the others still apply.
- Replay duplication: queue 1 "start session" action; while replaying, the
  server returns 200 with the original session ID. Re-issue the same
  action with the same `Idempotency-Key` 30 s later: the response is
  byte-identical (server-side cache window matches Story 7.1 spec).
- SW update test: deploy build B → existing tab gets a "Reload" toast;
  reload picks up B; the old SW is unregistered cleanly.

## EC

- Quota exhaustion (Safari ITP, 50 MB per origin): SW stops caching new
  responses with `quotaexceeded`; UI surfaces "Offline cache full — some
  data may be missing" non-blocking.
- iOS Safari quirk: SW killed after 30 s idle. We do not rely on long-lived
  workers for any critical path.
- A request to `POST /api/auth/login` is never queued (security: an
  offline replay of a login is meaningless); only idempotent or
  user-initiated state changes are queued.
- The server-side idempotency cache window (e.g., 24 h) expires before
  replay: replay produces a fresh row; we tolerate this rare double-create
  because the action is "save search" or "register device" — both
  natively dedup-able by content.
