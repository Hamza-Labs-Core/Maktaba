# Story 29.5 — Playback statistics per video

> Epic 29 · Watch Analytics · Phase 5 (per-video read)

## Description

The video detail page surfaces how a video has performed.

- **Stats.** `GET /api/videos/{id}/stats` returns aggregates over
  `watch_sessions` for that video:
  - `total_views` — count of sessions,
  - `unique_viewers` — distinct `user_id`,
  - `avg_completion` — mean `percent_complete`,
  - `avg_watch_sec` — mean `duration_sec`,
  - `completion_rate` — share of sessions with `state='completed'`,
  - `last_watched_at`.
- **Per-user breakdown (admin only).** When the caller is an admin the
  response additionally includes `viewers[]`: one entry per user
  (`user_id`, `username`, `times_watched`, `total_watch_sec`,
  `best_percent`, `last_watched_at`). Non-admins receive aggregates only
  — never another user's identity.
- **Web.** `VideoDetail.tsx` gains a "Statistics" section rendering the
  aggregate cards; for admins, an expandable per-viewer table.

## Acceptance criteria

- **Given** a video watched by 3 users across 5 sessions (2 completed),
  **when** any authenticated user calls `GET /api/videos/{id}/stats`,
  **then** `total_views=5`, `unique_viewers=3`, `completion_rate=0.4`,
  and `avg_completion`/`avg_watch_sec` are the means.

- **Given** the caller is an admin,
  **when** they fetch the stats,
  **then** the response also includes `viewers[]` with per-user totals.

- **Given** the caller is a regular user,
  **when** they fetch the stats,
  **then** `viewers` is absent (aggregates only).

- **Given** a video nobody has watched,
  **when** stats are fetched,
  **then** all counts are zero and the call still returns `200`.

## Notes

- Mounted on the Epic 29 router (`p29.go`) as
  `GET /api/videos/{id}/stats` rather than inside the `videos` package,
  keeping the epic diff isolated while still "extending" the video
  surface.
- The aggregate query is covered by the `watch_sessions(video_id)`
  index.
