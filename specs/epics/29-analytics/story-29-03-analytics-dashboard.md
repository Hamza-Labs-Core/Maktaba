# Story 29.3 — Analytics dashboard (admin)

> Epic 29 · Watch Analytics · Phase 3 (aggregate read)

## Description

An admin-only dashboard showing aggregate viewing statistics, built on
read-time `GROUP BY` queries over `watch_sessions` (joined to `users`,
`videos`, `video_tags`/`tags`, `libraries`) with a short in-memory cache.

### Cards / endpoints

- **Live — `GET /api/admin/analytics/live`.** Sessions currently
  `active` (heartbeat within the stale window): who is watching what,
  for how long, on which device. Drives the "Currently watching" card.
- **Summary — `GET /api/admin/analytics/summary?range=`.** Headline
  totals for the range (`today|7d|30d|90d|1y|all`, default `7d`): total
  watch hours, total sessions, unique viewers, completion rate, plus the
  device/platform breakdown, the library-usage breakdown, and the
  popular-genres list (from Epic 26 tags).
- **Top videos — `GET /api/admin/analytics/top-videos?range=&limit=`.**
  Most-watched videos by session count and by watch time, for
  all-time / this-week / today.
- **Activity — `GET /api/admin/analytics/activity?range=&bucket=`.**
  Time series of watch time per `day|week|month`, plus the
  **peak-hours heatmap** matrix (`day_of_week × hour_of_day` → watch
  seconds).
- **Users — `GET /api/admin/analytics/users?range=&limit=`.** Most
  active users by watch time, with session count and last-seen.

### Web

- `web/src/pages/Admin/Analytics.tsx`, gated by `AdminGate`, route
  `/admin/analytics`, linked from the admin nav. Cards: live, summary
  KPIs, watch-time line chart, top-videos table, active-users table,
  genre bars, peak-hours heatmap, device + library breakdown bars.
- Charts are lightweight inline SVG components (no new dependency — the
  web app intentionally ships a minimal dep set); the chart primitives
  live in `web/src/components/charts/`.

## Acceptance criteria

- **Given** two users watching right now,
  **when** an admin calls `GET /api/admin/analytics/live`,
  **then** both active sessions are listed with user, video, elapsed
  time and device; a session reaped as interrupted disappears.

- **Given** `?range=7d`,
  **when** `summary` is fetched,
  **then** totals cover only sessions started in the last 7 days, and
  the device/library/genre breakdowns sum consistently.

- **Given** sessions across several hours and days,
  **when** `activity?bucket=day` is fetched,
  **then** the series has one point per day in range and the heatmap
  matrix attributes watch seconds to the correct (weekday, hour) cell.

- **Given** a non-admin principal,
  **when** any `/api/admin/analytics/*` endpoint is called,
  **then** it returns `403`.

## Notes

- All five endpoints are **admin-gated in-handler** via
  `principal.FromContext` (the channel/p28 convention).
- Library-usage and top-videos respect nothing beyond admin scope —
  admins see the whole server (consistent with the other admin
  surfaces); per-library ACL scoping is out of scope for the dashboard.
- The summary response is cached in-memory ~30 s keyed by range to
  absorb refresh spam (D6).
