# Story 21.7 — Job and pipeline visibility

Self-hosters need to see "what is the pipeline doing right now" without
SSH'ing in.

The endpoints used by this story are owned by Epic 7
(`GET /api/queue/stats`, `GET /api/jobs/{id}`, `WS /ws/jobs`); this
story does **not** introduce a parallel `/api/processing/*` namespace.
It specifies the additional payload fields and supporting indexes
needed so the existing endpoints serve the operator-facing surface.

## Acceptance criteria

- AC1. `GET /api/queue/stats` (Epic 7 Story 7.13) returns:
  - counts by `(stage, state, library_id)`,
  - global queue depth (`pending` + `running`),
  - oldest pending job age (`now() - MIN(created_at) WHERE
    state='pending'`),
  - in-progress job count,
  - rolling 1-hour average wall-clock per stage,
  - last 50 errors (with `error_id`, `category`, `occurred_at`).
  These additional fields are added by this story to the existing
  endpoint, not a new one. Required indexes are owned by
  [Story 18.7 AC2](../18-performance/story-18-07-database-query-performance.md).
- AC2. `GET /api/jobs/{id}` (architecture §9.5) returns the full state-
  machine history, segment-by-segment progress for transcribe, and the
  last heartbeat timestamp.
- AC3. `WS /ws/jobs` (Epic 7 Story 7.16) carries per-job per-segment
  progress with ≤ 1 s end-to-end latency from segment commit to client
  paint. Subscriptions are scoped per job via a query parameter
  (`?job_id=<uuid>`); the WS surface is a single endpoint with
  filtered events, not one path per job.
- AC4. The admin panel page (web) renders the above as charts
  (queue-depth time series, throughput by stage) and a sortable job
  list with filter chips, all backed by the Epic 7 endpoints.

## Test cases

- TC1. Snapshot: with 100 jobs across all stages, the `queue/stats`
  endpoint returns counts that match a hand-counted SQL query and the
  index plan from Story 18.7 is hit.
- TC2. Live progress: drop a 60 s clip; `WS /ws/jobs?job_id=<id>` emits
  ≥ 6 progress events (one per ~10 s segment) before READY.
- TC3. Errored job: a transcribe with an unreadable file appears in
  `last_errors` with `error_id`, `category=ffmpeg`, and a link to the
  full job page.
- TC4. No parallel surface: a request to `/api/processing/status` or
  `/api/processing/jobs/*` returns 404 (the namespace is reserved
  unused; no story owns it). A CI route-list lint asserts no
  `/api/processing/*` route is registered.

## Edge cases

- EC1. Long-stalled job (heartbeat > 3× interval) is shown as
  `state=stuck` in the UI even though the DB still says `running`.
- EC2. WS subscriber count > 100 on a single job — fan-out batches
  per-second to avoid amplification.
- EC3. Privileged data (full file path) — masked to a per-library
  relative path for non-admin users.
