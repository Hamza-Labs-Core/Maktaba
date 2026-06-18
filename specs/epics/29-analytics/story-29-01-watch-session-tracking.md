# Story 29.1 — Watch session tracking

> Epic 29 · Watch Analytics · Phase 1 (collection)

## Description

Every play is recorded as a **watch session**: a row opened when
playback starts, advanced by a periodic heartbeat, and closed when
playback stops. This is the fact table the rest of the epic reads.

Behaviour:

- **Start.** `POST /api/watch/start {video_id, device_type?, platform?,
  quality?}` inserts a `watch_sessions` row (`state='active'`,
  `started_at = last_heartbeat = now`, `duration_sec = 0`) and returns
  `{session_id}`. The client IP is salted-hashed into `ip_addr_hash`;
  the raw IP is never stored. If the caller has **paused tracking**
  (`user_analytics_prefs.track_enabled = false`, Story 29.4) the
  endpoint is a no-op that returns `{session_id: null, tracking: false}`
  — no row is written.
- **Heartbeat.** `POST /api/watch/heartbeat {session_id, position_sec,
  duration_watched_sec?}` advances `last_heartbeat = now`, recomputes
  `percent_complete` from `position_sec / video.duration_sec`, and adds
  the elapsed *live* watched time to `duration_sec`. Clients send one
  every **30 s**. A heartbeat for a non-active session (already
  stopped/interrupted) re-opens nothing — it returns `409`.
- **Stop.** `POST /api/watch/stop {session_id, position_sec?}` sets
  `ended_at = now`, `state = 'completed'` if `percent_complete ≥ 95`
  else `'stopped'`, and applies a final heartbeat. Idempotent: stopping
  an already-closed session returns the closed row unchanged.
- **Interrupted sessions.** A background **reaper** runs every minute and
  marks any `active` session whose `last_heartbeat` is older than the
  **stale timeout (default 5 min)** as `state='interrupted'`,
  `ended_at = last_heartbeat`. This keeps "currently watching" honest
  and bounds the active set.

## Acceptance criteria

- **Given** an authenticated user plays a 600 s video,
  **when** they `POST /api/watch/start`,
  **then** a `watch_sessions` row exists with `state='active'`,
  `started_at` set, `duration_sec=0`, and the response carries a
  `session_id`.

- **Given** an active session at `position_sec=0`,
  **when** a heartbeat arrives with `position_sec=300` after ~300 s,
  **then** `percent_complete≈50`, `duration_sec` reflects the watched
  time, and `last_heartbeat` is advanced.

- **Given** an active session,
  **when** `POST /api/watch/stop` arrives at `position_sec=580`
  (≥95 % of 600),
  **then** `state='completed'`, `ended_at` is set, and a second stop is
  a no-op returning the same row.

- **Given** an active session whose last heartbeat was 6 minutes ago,
  **when** the reaper runs,
  **then** the session becomes `state='interrupted'` with
  `ended_at = last_heartbeat`, and it no longer appears in the live view.

- **Given** a user who has paused tracking,
  **when** they `POST /api/watch/start`,
  **then** no row is written and the response is `{tracking:false}`.

## Notes

- The heartbeat is the unit of truth for *watched* time (D3): pause time
  between heartbeats is not credited; a gap longer than the stale
  timeout is closed by the reaper, not accumulated.
- `duration_sec` is clamped so a single heartbeat can never add more
  than `stale_timeout` of credited time (guards clock jumps / replays).
- Endpoints are body-limited and require an authenticated principal;
  start/heartbeat/stop are owner-scoped (a session belongs to its
  creator).
