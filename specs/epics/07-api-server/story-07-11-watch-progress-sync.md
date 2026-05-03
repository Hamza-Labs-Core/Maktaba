# Story 7.11 — Watch progress sync

`POST /api/stream/sessions/{id}/progress` from §9.4 + WebSocket fan-out
on `/ws/playback/{video_id}` (story 7.16).

**AC-1 — Persist progress (debounced upsert).**
- **Given** an open session,
- **When** `POST /api/stream/sessions/{id}/progress` is called with
  `{position_sec, completed?}`,
- **Then** `playback_state (user_id, video_id)` is upserted (debounced
  per AC-3 to at most one persisted upsert per second per session) with
  the new position, `completed = true` is auto-set when `position_sec /
  duration_sec > 0.95`, and `updated_at = now()`.

**AC-2 — Fan out to other devices.**
- **Given** a user with two devices subscribed to
  `/ws/playback/{video_id}`,
- **When** progress is POSTed from device A,
- **Then** device B receives a frame `{type: "playback.progress",
  user_id, video_id, position_sec, completed, source_session_id}` within
  500 ms p95, including the `source_session_id` so device B can ignore
  echoes if it sent the original.

**AC-3 — Rate limit the firehose.**
- **Given** a player POSTing progress every 100 ms (misconfigured),
- **When** more than 1 POST per second is received per session,
- **Then** the additional POSTs are accepted with 200 OK but only the
  last per second is persisted (debounced server-side); WS fan-out
  matches the persistence cadence.

**AC-4 — Stale POSTs accepted (no monotonicity check).**
- **Given** a current stored `position_sec = 450`,
- **When** a POST arrives with `position_sec = 200` (the user manually
  rewound, or playback is resuming on a second device that hasn't
  caught up),
- **Then** the upsert is **accepted** and the new position is stored.
  This is intentional: resume-everywhere UX requires that scrubbing
  backward is a normal operation. The corresponding NFR (Epic 24.4)
  must not enforce monotonicity for `position_sec` updates.

**Test cases:**
- Integration: position update arrives at the other device's WS within
  500 ms in a local docker compose env.
- Integration: `completed = true` triggers a separate `playback.completed`
  WS event in addition to the progress event, and updates `playback_state`
  in one transaction.
- Integration: a stale POST with `position_sec` lower than the current
  stored position is **still accepted** (user manually rewound) — no
  monotonicity check, even if `seek=true` is omitted.
- Integration: 10 POSTs in 1 s for the same session → exactly one DB
  write; the last POST's position is what persists.

**Edge cases:**
- POST after `DELETE /sessions/{id}` — accepted with 200, persisted to
  `playback_state` (the watch happened, even if the session is closed).
  The closed `streaming_sessions` row is not reused; `playback_state`
  is keyed on `(user_id, video_id)`, not session.
- POST with `position_sec` greater than `duration_sec` is clamped, and a
  warning header is added.
- Network jitter causes POSTs to arrive out of order — the persistence
  uses `updated_at = now()` not the client clock, so the "latest received"
  wins, not the "latest in time."
- Disconnected client comes back online and bulk-replays 30 progress POSTs
  — the rate limiter (1/s) only persists ~30 entries spread over time;
  the final position is correct, intermediate ones may be coarsened.
