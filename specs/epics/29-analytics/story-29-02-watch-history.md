# Story 29.2 — Watch history

> Epic 29 · Watch Analytics · Phase 2 (per-user read)

## Description

Every user can see and manage their own watch history, with the resume
position kept consistent with "Continue Watching".

- **List.** `GET /api/me/history?limit=&offset=&from=&to=` returns the
  caller's watched videos, most-recent first, paginated. Each entry
  carries the video summary, `last_watched_at`, `times_watched` (count
  of sessions), `total_watch_sec`, the best `percent_complete`, a
  `completed` flag, and the **resume `position_sec`** read from
  `playback_state` (D2). `from`/`to` are inclusive RFC3339 date bounds
  on `started_at`.
- **Per-video state.** `GET /api/me/history/{video_id}` returns the
  caller's watch state for one video: resume `position_sec`, `completed`,
  `times_watched`, `total_watch_sec`, `first_watched_at`,
  `last_watched_at`, and the session list.
- **Delete.** `DELETE /api/me/history/{video_id}` removes the caller's
  history for that video — deletes their `watch_sessions` rows and the
  `playback_state` row, so it also drops out of "Continue Watching".
  Returns `204`.
- **Continue Watching wiring.** The home rail already reads
  `playback_state` (Epic 7/14). History reuses that exact source for the
  resume column so deleting history and the rail stay in lockstep; no
  rail query changes are needed.

## Acceptance criteria

- **Given** a user with three watched videos,
  **when** they `GET /api/me/history`,
  **then** the three appear most-recent first, each with
  `position_sec` matching their `playback_state` and a `times_watched`
  count.

- **Given** `?from=2026-06-01&to=2026-06-10`,
  **when** the list is fetched,
  **then** only sessions started within that inclusive window are
  counted.

- **Given** a video the user has watched,
  **when** they `DELETE /api/me/history/{video_id}`,
  **then** it returns `204`, disappears from `GET /api/me/history`, and
  is gone from the Continue Watching rail.

- **Given** another user's video_id,
  **when** the caller deletes it,
  **then** only the caller's own rows are affected (owner-scoped).

## Notes

- History is strictly owner-scoped: a principal only ever sees its own
  rows. The admin per-user view lives in 29.3/29.5, gated separately.
- Pagination mirrors the existing `paginate` helper conventions
  (`limit` default 50, max 200; `offset`).
