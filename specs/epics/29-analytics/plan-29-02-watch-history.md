# Implementation Plan — Story 29.2 Watch history

> Companion to [story-29-02-watch-history.md](story-29-02-watch-history.md).

## 0. Placement

`api/internal/handlers/watch/history.go` (same package as 29.1; shares
the repo). Routes mounted in `p29.go` on the `/api/me` group:

```
GET    /api/me/history
GET    /api/me/history/{video_id}
DELETE /api/me/history/{video_id}
```

## 1. Queries (owner-scoped, `$N` placeholders)

**List** — group sessions per video, left-join `playback_state` for the
resume point (D2) and `videos` for the summary:

```sql
SELECT v.id, v.title, v.duration_sec,
       COUNT(ws.id)                      AS times_watched,
       COALESCE(SUM(ws.duration_sec),0)  AS total_watch_sec,
       MAX(ws.percent_complete)          AS best_percent,
       MAX(ws.started_at)                AS last_watched_at,
       COALESCE(ps.position_sec,0)       AS position_sec,
       COALESCE(ps.completed,false)      AS completed
FROM watch_sessions ws
JOIN videos v          ON v.id = ws.video_id
LEFT JOIN playback_state ps ON ps.user_id = ws.user_id AND ps.video_id = ws.video_id
WHERE ws.user_id = $1
  AND ($2::timestamptz IS NULL OR ws.started_at >= $2)
  AND ($3::timestamptz IS NULL OR ws.started_at <= $3)
GROUP BY v.id, v.title, v.duration_sec, ps.position_sec, ps.completed
ORDER BY last_watched_at DESC
LIMIT $4 OFFSET $5;
```

(SQLite drops the `::timestamptz` casts — build the predicate
conditionally in Go so one code path serves both, matching the
`videos.placeholder` pattern.)

**Per-video** — same group restricted to one `video_id`, plus the
session list.

**Delete** — a transaction: `DELETE FROM watch_sessions WHERE user_id=$1
AND video_id=$2; DELETE FROM playback_state WHERE user_id=$1 AND
video_id=$2;` → `204`.

## 2. DTOs

`HistoryItem{VideoID, Title, DurationSec, TimesWatched, TotalWatchSec,
BestPercent, LastWatchedAt, PositionSec, Completed}`; list response
`{items, limit, offset}`.

## 3. Continue-Watching wiring

No change to `recommendations.continueRail` — it already reads
`playback_state`. History's delete removes the `playback_state` row, so
the rail and history stay consistent by construction. Documented as a
test asserting delete clears both.

## 4. Pagination

Reuse `paginate` conventions: `limit` default 50, clamp ≤200; `offset`
≥0. `from`/`to` parsed with `time.Parse(time.RFC3339, …)` (accept bare
date by appending `T00:00:00Z`).

## 5. Tests

- Date-window predicate builder (pure): given from/to present/absent,
  the right arg slice + SQL fragment is produced.
- Limit/offset clamping.
