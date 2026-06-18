# Implementation Plan — Story 29.3 Analytics dashboard (admin)

> Companion to [story-29-03-analytics-dashboard.md](story-29-03-analytics-dashboard.md).

## 0. Placement

`api/internal/handlers/analytics/` — admin aggregate reads:

```
GET /api/admin/analytics/live
GET /api/admin/analytics/summary?range=
GET /api/admin/analytics/top-videos?range=&limit=
GET /api/admin/analytics/activity?range=&bucket=
GET /api/admin/analytics/users?range=&limit=
```

Mounted in `p29.go`. Every handler: `principal.FromContext`; `403` unless
`p.IsAdmin`.

## 1. Range parsing (pure, `ranges.go`)

```go
// ParseRange maps today|7d|30d|90d|1y|all to a start cutoff relative to
// now; "all" ⇒ zero time. Default 7d on empty/invalid.
func ParseRange(s string, now time.Time) (start time.Time, label string)
```

Unit-tested in isolation (boundaries, default, "all").

## 2. Queries (`repo.go`)

- **live**: `SELECT ws.*, u.username, v.title FROM watch_sessions ws
  JOIN users u… JOIN videos v… WHERE state='active' AND last_heartbeat >
  $stale ORDER BY started_at`.
- **summary**: several cheap aggregates over `started_at >= $start`:
  total hours (`SUM(duration_sec)`), sessions (`COUNT`), unique viewers
  (`COUNT(DISTINCT user_id)`), completion rate
  (`AVG(state='completed')`); device breakdown (`GROUP BY device_type`);
  platform breakdown; library breakdown (`JOIN videos … GROUP BY
  library_id`); genres (`JOIN video_tags vt ON vt.video_id=ws.video_id
  JOIN tags t … GROUP BY t.name ORDER BY … LIMIT 12`).
- **top-videos**: `GROUP BY video_id` ordered by `COUNT` and by
  `SUM(duration_sec)`, `LIMIT`.
- **activity**: time series — `GROUP BY date_trunc($bucket, started_at)`
  (PG) / `strftime` (SQLite); heatmap — `GROUP BY EXTRACT(dow…),
  EXTRACT(hour…)` into a 7×24 matrix assembled in Go.
- **users**: `GROUP BY user_id` ordered by `SUM(duration_sec)`, with
  `MAX(started_at)` last-seen and `COUNT` sessions, `LIMIT`.

Bucket/dow/hour SQL differs per dialect → a tiny `dialect` switch keyed
off a `driverName` passed into the repo (detected once at construction;
default `postgres`).

## 3. Summary cache (`cache.go`)

`sync.RWMutex` map keyed by range label, 30 s TTL (D6); `?refresh=true`
bypasses. Mirrors `streaming.capCache`.

## 4. Web

- `web/src/lib/analytics.ts` — TS contracts + `analyticsApi` helpers
  (mirrors `lib/channels.ts`).
- `web/src/components/charts/` — inline-SVG primitives: `LineChart`,
  `BarList`, `Heatmap`, `Sparkline`. No new dependency.
- `web/src/pages/Admin/Analytics.tsx` — `AdminGate` + cards:
  live, KPI summary, watch-time line, top-videos table, active-users
  table, genre `BarList`, peak-hours `Heatmap`, device/library
  `BarList`. Range selector drives a refetch. `useI18n` for all copy.
- Route `/admin/analytics` in `App.tsx`; admin nav link.
- i18n keys `admin.analytics.*` added to EN + AR (parity preserved).

## 5. Tests

- `ParseRange` table test.
- `buildHeatmap` matrix assembly (pure: rows of {dow,hour,sec} → 7×24).
- Web: a render smoke test for the chart primitives (Vitest) is optional
  but the dashboard page itself is data-driven and excluded from unit
  scope.
