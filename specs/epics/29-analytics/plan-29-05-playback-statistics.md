# Implementation Plan — Story 29.5 Playback statistics per video

> Companion to [story-29-05-playback-statistics.md](story-29-05-playback-statistics.md).

## 0. Placement

`api/internal/handlers/analytics/videostats.go` (admin breakdown lives
naturally beside the other aggregate reads). Route in `p29.go`:

```
GET /api/videos/{id}/stats
```

Authenticated principal required; admin extras gated in-handler.

## 1. Queries

**Aggregate** (everyone):

```sql
SELECT COUNT(*)                               AS total_views,
       COUNT(DISTINCT user_id)                AS unique_viewers,
       COALESCE(AVG(percent_complete),0)      AS avg_completion,
       COALESCE(AVG(duration_sec),0)          AS avg_watch_sec,
       COALESCE(AVG(CASE WHEN state='completed' THEN 1.0 ELSE 0 END),0) AS completion_rate,
       MAX(started_at)                         AS last_watched_at
FROM watch_sessions WHERE video_id = $1;
```

**Per-user** (admin only):

```sql
SELECT ws.user_id, u.username,
       COUNT(*)                 AS times_watched,
       SUM(ws.duration_sec)     AS total_watch_sec,
       MAX(ws.percent_complete) AS best_percent,
       MAX(ws.started_at)       AS last_watched_at
FROM watch_sessions ws JOIN users u ON u.id = ws.user_id
WHERE ws.video_id = $1
GROUP BY ws.user_id, u.username
ORDER BY total_watch_sec DESC;
```

## 2. Response shape

```go
type VideoStats struct {
    TotalViews      int      `json:"total_views"`
    UniqueViewers   int      `json:"unique_viewers"`
    AvgCompletion   float64  `json:"avg_completion"`
    AvgWatchSec     float64  `json:"avg_watch_sec"`
    CompletionRate  float64  `json:"completion_rate"`
    LastWatchedAt   *string  `json:"last_watched_at,omitempty"`
    Viewers         []Viewer `json:"viewers,omitempty"` // admin only
}
```

Empty table ⇒ all zeros, `200` (AC). `video_id` validated as uuid.

## 3. Web

`VideoDetail.tsx` gains a **Statistics** section:
- a row of stat cards (views, unique viewers, avg completion %, avg
  watch time) using existing DS `Card`,
- for admins, an expandable per-viewer table (rendered only when
  `viewers` is present).
- Fetched via `analyticsApi.videoStats(id)`; i18n `video.stats.*`.

## 4. Tests

- Pure formatter for `avg_completion`/`completion_rate` → display
  strings (e.g. `0.4` → `40%`), and the zero-state shaping.
