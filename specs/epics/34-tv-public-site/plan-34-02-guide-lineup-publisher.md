# Plan 34.2 — Guide and lineup publisher (real time) — implementation

> Implementation plan for [story-34-02](story-34-02-guide-lineup-publisher.md).
> Uses the Epic 34 migration (`tv_public_publishes`; slot assigned at implementation). Design: [design.md](design.md) §3.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Publish through the Worker's `/_home/publish/*`, not to R2 directly. | One validation point, atomic commit + fan-out in the same call, no R2 S3 keys on the home. |
| D2 | Content-addressed channel-day blobs + small index deltas. | A schedule edit sends one or two ~40 KB blobs; immutable caching at the edge. |
| D3 | Per-channel debounce 2 s, queue-engine job per window. | Meets the 10 s target while coalescing bursts; runs visible in the control centre. |
| D4 | SiteControl holds the current snapshot id; pages get events over LiveHub. | No stale cache window; no polling of the home; open guides update in seconds. |
| D5 | Now/next computed in the browser with a server clock offset. | Zero requests per tick; correct on wrong device clocks. |
| D6 | Drift re-times the published items only. | The schedule (Epic 27) stays the source; the public guide shows what really airs. |

## 1. Files

**Create**
- `api/internal/tvpublic/publisher.go` — listener (NOTIFY `tv_schedule_changed`, `tv_public_changed`,
  `channel_runtime` drift), per-channel debounce, job enqueue
- `api/internal/tvpublic/snapshot.go` — build `site.json`, `lineup.json`, channel-day blobs (uses
  `handlers/guide/repo.go` queries for program rows), image resizing (logos ≤ 512 px, posters ≤ 640 px, WebP)
- `api/internal/tvpublic/homeclient.go` — HMAC client for `/_home/*` (HEAD/POST blob, commit, retries)
- `api/internal/tvpublic/schema/snapshot.schema.json` — shared with the Worker (copied at build)
- `api/internal/handlers/tvpublic/publish.go` — `POST /api/tv/public/publish`
- `web/tv-site/worker/src/home.ts` (publish routes, validation, R2 writes), `web/tv-site/worker/src/livehub.ts`
- `web/tv-site/src/lib/guide/*` — index/blob loader, clock offset, now/next, live socket with fallback poll
- `web/tv-site/test/no-home-links.test.ts`
- tests

**Modify**
- Epic 27 scheduler write path and Epic 33 commit/hot-swap path — `pg_notify('tv_schedule_changed',
  json{channel_id, from, to, reason})` (one line each where blocks are written)
- `streaming/internal/channel/station.go` — report actual block start/end to `channel_runtime` (already
  partly there) so drift can be detected
- queue engine job registry — kind `tv_public_publish` (modes `full`, `delta`)

## 2. Steps

1. Snapshot builder and schema; golden-file tests for blobs (ar/en titles, `a:false`).
2. Worker publish endpoints with schema validation, size caps, optimistic `base`, R2 writes, SiteControl
   commit, LiveHub fan-out.
3. Home client with HMAC; known-sha cache (LRU 50k) to skip HEADs.
4. Listener + debounce + job; full refresh cron every 6 h; GC of snapshots older than 24 h (Worker cron).
5. Drift detection (> 5 s) and re-timing for the rest of the day.
6. Site guide loader: index by snapshot, blob fetch, clock offset from `X-Server-Time`, LiveHub socket,
   15 s version poll fallback.
7. E2E latency test (change → page) with a p95 assertion of 10 s on CI hardware.

## 3. Tests

Story TCs; plus `TestSnapshotHasNoForbiddenFields` (paths, ids, hosts) as a property test over random
schedules.

## 4. Rollout

Phase A: publisher on, site guide-only. Watch the control centre for publish durations; the 10 s target
is tracked by the e2e test and by `publish_to_page_ms` events from open pages.
