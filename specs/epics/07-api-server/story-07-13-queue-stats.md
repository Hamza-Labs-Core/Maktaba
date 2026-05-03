# Story 7.13 — Queue stats endpoint

`GET /api/queue/stats` from §9.5.

> **Canonical naming.** This is the single canonical queue-stats surface.
> NFR Story 21.7's `GET /api/processing/status` is a duplicate that
> should be removed; consumers target `/api/queue/stats`.

**AC-1 — Shape.**
- **Given** any queue state,
- **When** the endpoint is called,
- **Then** the response is `{by_stage: {scan: {pending, running, paused,
  failed, done_24h}, probe: {...}, ...}, eta_sec: N, total_in_flight: N,
  oldest_pending_age_sec: N, workers: [{id, host, last_heartbeat,
  current_job_id}]}`.

**AC-2 — Required indexes.**
- **Given** the queries that drive AC-1,
- **When** the schema is migrated,
- **Then** `processing_jobs` has the following indexes (added by Pipeline
  Story 6.1 with this story as a hard dependency):
  - `(state, finished_at) WHERE state IN ('done','failed')` — drives
    `done_24h` per stage,
  - `(state, created_at) WHERE state = 'pending'` — drives
    `oldest_pending_age_sec` and is also used by Pipeline's claim loop,
  - `(state, claimed_by, last_heartbeat_at) WHERE state = 'running'` —
    drives the workers table.

**Test cases:**
- Unit: `eta_sec` is a sum of `estimated_remaining_sec` over `running`
  jobs, divided by per-stage parallelism.
- Integration: the query is one SQL round trip with stage counts via
  `GROUP BY stage, state`.
- Performance: under 30 ms on a 100k-job table (only counts, no scans of
  big rows).

**Edge cases:**
- A stage has no jobs at all — included in the response with all zeros so
  the UI doesn't have to special-case the missing key.
- Workers heartbeat field is null for stale workers — surfaced in the
  response so the UI can highlight a "missing worker" warning.
