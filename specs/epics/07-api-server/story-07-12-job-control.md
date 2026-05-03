# Story 7.12 — Job control endpoints

`POST /api/jobs/{id}/{pause,resume,cancel,retry}` and the per-video
shortcuts from §9.5 + §7.7. Idempotent flag-setters; never block on the
worker.

> **Canonical naming.** This is the single canonical job-control surface.
> Any `/api/processing/*` references in NFR or other epics are duplicates
> and should target `/api/jobs/*` and `/api/queue/stats` instead.

**AC-1 — Pause sets the flag, returns immediately.**
- **Given** a `running` job,
- **When** `POST /api/jobs/{id}/pause` is called,
- **Then** `pause_requested = true` is set in one UPDATE, the response is
  200 with the current job row (state still `running`), and a Postgres
  NOTIFY fires on `jobs.flag_set`. The actual state transition to
  `paused` happens asynchronously in the worker (§7.7) and is observed by
  the client over WS.

**AC-2 — Force pause.**
- **Given** a `running` job stuck inside a single segment for ≥
  `pause_grace_sec`,
- **When** `POST /api/jobs/{id}/pause?force=true` is called,
- **Then** the API directly UPDATEs `state='paused', paused_reason='user-force',
  paused_at_sec=last_segment_end_sec, claimed_by=NULL, pause_requested=false`,
  and the in-flight segment is discarded as documented in §7.7.

**AC-3 — Resume is a flag-clear.**
- **Given** a `paused` job,
- **When** `POST /api/jobs/{id}/resume` is called,
- **Then** the row's `paused_reason` is cleared (the job becomes
  re-claimable per §7.3), and the response is 200 with the unchanged
  state. The actual claim happens asynchronously.

**AC-4 — Cancel.**
- **Given** any non-terminal job,
- **When** `POST /api/jobs/{id}/cancel` is called,
- **Then** `cancel_requested = true` is set; the worker observes it after
  the next segment commit and transitions to `cancelled` (§7.7).

**AC-5 — Retry.**
- **Given** a `failed` job,
- **When** `POST /api/jobs/{id}/retry` is called,
- **Then** `attempts` is reset to 0, `state` flips to `pending`, `error`
  is cleared, and `not_before` is set to `now()` (cancels any backoff).
- **Given** a non-`failed` job, **Then** the response is `409 conflict`
  `type: job-not-failed`.

**AC-6 — Per-video aggregates.**
- **Given** a video with three active jobs across stages,
- **When** `POST /api/videos/{id}/pause` is called,
- **Then** every non-terminal job for that video has `pause_requested =
  true` set in one UPDATE, and the response includes `affected: 3`.

**AC-7 — Idempotency.**
- **Given** a job in any state,
- **When** the same control call is made twice,
- **Then** both responses are 200 with the same body (no error, no
  double-effect).

**Test cases:**
- Unit: state-machine guards reject illegal transitions (e.g. force-pause
  on a `done` job → 409 `type: job-terminal`).
- Integration: pause-then-resume cycle within 100 ms returns the job to
  `running` (worker re-claims it).
- Integration: NOTIFY on `jobs.flag_set` is observed by a listener test.
- Integration: per-video `pause` against a video with five jobs at mixed
  states only flips the non-terminal ones and reports the count.
- Integration: control endpoints take < 20 ms p99 under load (DB-only,
  no gRPC, no worker round-trip).

**Edge cases:**
- Pause on a `pending` job — sets `pause_requested = true`; the claim
  loop respects the flag and the job stays pending. Effectively a "freeze
  in queue" semantics. Document in the API reference.
- Resume on a `running` job — no-op; returns 200 with current state.
- Cancel on a `done` job — 409 `type: job-terminal`. Same for retry on
  `running`/`pending`.
- Force-pause race: the worker commits a segment while the API is
  setting `paused_at_sec=last_segment_end_sec` — the API uses a single
  UPDATE with the read inside (no read-then-write), so the value is
  always consistent.
- Mass per-video resume with 50 jobs — the UPDATE is one statement; the
  worker pool may not pick all up immediately (concurrency caps), so the
  response carries `affected` (DB rows updated), not `restarted`.
