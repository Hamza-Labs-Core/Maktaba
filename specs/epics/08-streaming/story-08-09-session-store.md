# Story 8.9 — Session store, sticky transcoder, reaper

`streaming_sessions` table holds session metadata. Each session is pinned
to one Streaming process (the one that owns the FFmpeg). In multi-host
deployments, sticky routing (consistent-hash on `session_id` cookie)
keeps the player coming back to the same box per §10.3.

**AC-1 — Session row shape.**
- **Given** an open session,
- **When** the row is inspected,
- **Then** it has the columns from the schema in [README.md](README.md):
  `{id, video_id, user_id, client_profile, mode, format, host, pid,
  started_at, last_segment_at, closed_at, closed_reason, state}`.
- The `videos.library_id → libraries.id` cascade chain ensures that
  when Epic 9 Story 9.15 deletes a library, its `streaming_sessions`
  rows are removed (after the API closes them via gRPC first).

**AC-2 — Last-segment heartbeat.**
- **Given** a player fetching segments,
- **When** any segment is served,
- **Then** `last_segment_at = now()` is updated atomically (batched at
  most once per 5 s per session to avoid a write storm).

**AC-3 — Reaper.**
- **Given** the reaper runs every 30 s,
- **When** it finds sessions with `last_segment_at < now() - 90 s` AND
  `closed_at IS NULL`,
- **Then** the FFmpeg is killed, the cache dir is purged, the row is
  marked `closed_at=now(), closed_reason='idle'`, and a metric
  `sessions_reaped_idle_total` is incremented.

**AC-4 — Cross-host stickiness.**
- **Given** a multi-host deployment,
- **When** a session is opened on host A,
- **Then** subsequent segment requests with the session's signed URL must
  reach host A. Achieved via a sticky cookie set on the manifest response
  + L7 LB consistent-hash policy. Misrouted requests return 421
  `Misdirected Request` so the LB can re-route.

**Test cases:**
- Unit: heartbeat batching — 100 segment fetches in 1 s produce 1 DB
  UPDATE.
- Integration: reaper kills an idle session within 30 s of the 90 s
  threshold.
- Integration: a misdirected request to host B for a host-A session
  returns 421 with the canonical-host hint.
- Integration: a closed session's cache dir is gone within 1 s of close.

**Edge cases:**
- A session whose owning Streaming binary crashed — the reaper on any
  Streaming binary that picks up the row (state `last_segment_at` stale
  + no PID match) marks it `closed_reason='crash'` and tries to clean
  the local cache dir. Cross-host cache cleanup is skipped (each box
  cleans its own).
- Player paused for > 90 s — session is reaped; on play resume the
  player must reopen the session (web player auto-detects 401 on next
  segment and calls `POST /api/stream/sessions`).
- `last_segment_at` updates colliding with reaper read — reaper uses
  `SELECT … FOR UPDATE SKIP LOCKED` against rows whose `last_segment_at <
  threshold`, so a fresh write between read and update simply means the
  next reaper tick picks it up.
