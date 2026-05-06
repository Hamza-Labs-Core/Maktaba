# Epic 06 — Job Queue

**Phase.** Pipeline (M0 — Foundation, blocks every other pipeline epic).
**Owner.** Pipeline Service ·
`pipeline/src/maktaba_pipeline/pipeline/runner.py` (claim loop, worker)
and `pipeline/src/maktaba_pipeline/db/jobs.py` (SQL).

> **Goal.** Implement the durable, atomic, pause-aware job queue that
> every other pipeline stage rides on. The queue is a single Postgres
> table (`processing_jobs`), not a broker, not Celery, not Redis. All
> claim, heartbeat, retry, pause, resume, cancel, and reaper logic lives
> here, and the stages above just call into it.

Source: [README](../../../specs/epics/06-job-queue/README.md) ·
Architecture §7 (Batch Processing).

---

## Stories

| # | Title | Priority | Linear | Story | Plan |
|---|-------|----------|--------|-------|------|
| 6.1 | Schema, migration, indexes | Gate | [HLB-36](../linear-map.md) | [story-06-01](../../../specs/epics/06-job-queue/story-06-01-schema-indexes.md) | [plan-06-01](../../../specs/epics/06-job-queue/plan-06-01-schema-indexes.md) |
| 6.2 | Claim loop | Gate | [HLB-37](../linear-map.md) | [story-06-02](../../../specs/epics/06-job-queue/story-06-02-claim-loop.md) | [plan-06-02](../../../specs/epics/06-job-queue/plan-06-02-claim-loop.md) |
| 6.3 | Heartbeat & progress (`jobs.*` channels) | Gate | [HLB-38](../linear-map.md) | [story-06-03](../../../specs/epics/06-job-queue/story-06-03-heartbeat-progress.md) | [plan-06-03](../../../specs/epics/06-job-queue/plan-06-03-heartbeat-progress.md) |
| 6.4 | Pause, resume, cancel via request flags | Core | [HLB-39](../linear-map.md) | [story-06-04](../../../specs/epics/06-job-queue/story-06-04-pause-resume-cancel.md) | [plan-06-04](../../../specs/epics/06-job-queue/plan-06-04-pause-resume-cancel.md) |
| 6.5 | Backoff and retry | Core | [HLB-40](../linear-map.md) | [story-06-05](../../../specs/epics/06-job-queue/story-06-05-backoff-retry.md) | [plan-06-05](../../../specs/epics/06-job-queue/plan-06-05-backoff-retry.md) |
| 6.6 | Reaper for crashed claims | Core | [HLB-41](../linear-map.md) | [story-06-06](../../../specs/epics/06-job-queue/story-06-06-reaper.md) | [plan-06-06](../../../specs/epics/06-job-queue/plan-06-06-reaper.md) |
| 6.7 | Concurrency model & per-host caps | Core | [HLB-42](../linear-map.md) | [story-06-07](../../../specs/epics/06-job-queue/story-06-07-concurrency-caps.md) | [plan-06-07](../../../specs/epics/06-job-queue/plan-06-07-concurrency-caps.md) |
| 6.8 | Graceful shutdown semantics | Core | [HLB-43](../linear-map.md) | [story-06-08](../../../specs/epics/06-job-queue/story-06-08-graceful-shutdown.md) | [plan-06-08](../../../specs/epics/06-job-queue/plan-06-08-graceful-shutdown.md) |
| 6.9 | Observability hooks | Core | [HLB-44](../linear-map.md) | [story-06-09](../../../specs/epics/06-job-queue/story-06-09-observability.md) | [plan-06-09](../../../specs/epics/06-job-queue/plan-06-09-observability.md) |
| 6.10 | Single source of truth for resume | Gate | [HLB-45](../linear-map.md) | [story-06-10](../../../specs/epics/06-job-queue/story-06-10-resume-invariant.md) | [plan-06-10](../../../specs/epics/06-job-queue/plan-06-10-resume-invariant.md) |

> Linear IDs from [linear-map.md](../linear-map.md).

### Related mockups & diagrams

| Story | Mockup | Diagram |
|-------|--------|---------|
| 6.1 | — | [job-lifecycle.drawio](../../../specs/diagrams/job-lifecycle.drawio) · [entity-relationship.drawio](../../../specs/diagrams/entity-relationship.drawio) |
| 6.2, 6.3 | [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) · [mockup-11-05-processing-queue](../../../web/mockups/mockup-11-05-processing-queue.html) | [job-lifecycle.drawio](../../../specs/diagrams/job-lifecycle.drawio) |
| 6.4 | [admin/job-pipeline.html](../../../web/mockups/admin/job-pipeline.html) | [job-lifecycle.drawio](../../../specs/diagrams/job-lifecycle.drawio) |
| 6.5, 6.6 | [admin/log-viewer.html](../../../web/mockups/admin/log-viewer.html) | [job-lifecycle.drawio](../../../specs/diagrams/job-lifecycle.drawio) |
| 6.7 | [admin/admin-dashboard.html](../../../web/mockups/admin/admin-dashboard.html) | [job-lifecycle.drawio](../../../specs/diagrams/job-lifecycle.drawio) |
| 6.8 | — | [job-lifecycle.drawio](../../../specs/diagrams/job-lifecycle.drawio) |
| 6.9 | [admin/admin-dashboard.html](../../../web/mockups/admin/admin-dashboard.html) · [admin/log-viewer.html](../../../web/mockups/admin/log-viewer.html) | — |
| 6.10 | — | [job-lifecycle.drawio](../../../specs/diagrams/job-lifecycle.drawio) |

---

## DB tables owned

**`processing_jobs`** — the only table this epic owns. Architecture §7.1.

Key columns: `id`, `video_id`, `stage`, `state`, `priority`,
`not_before`, `claimed_by`, `claimed_at`, `attempts`, `max_attempts`,
`last_heartbeat_at`, `progress_updated_at`, **`last_segment_end_sec`**
(canonical resume offset — Story 6.10), `processed_seconds`,
`total_duration_seconds`, `segments_completed`, `realtime_factor`,
`estimated_remaining_sec`, `pause_requested`, `cancel_requested`,
`paused_at`, `paused_at_sec`, `paused_reason`, `error` (JSON),
`finished_at`.

Indexes: `(state, priority, not_before)` for the claim path; `(video_id,
stage)` for lookups; partial `(state, last_heartbeat_at)
WHERE state IN (live-states)` for the reaper; partial
`(pause_requested) WHERE true` for flag polling.

Stage enum (`scan`, `probe`, `extract`, `transcribe`, `subtitle_gen`,
`index`, `thumbnail`) is owned by Epic 1 Story 1.6 — this epic depends on
that schema.

---

## API endpoints owned

**None directly.** The control endpoints
(`POST /api/jobs/{id}/pause|resume|cancel`, `GET /api/queue/stats`) are
implemented in the API service (Epic 7) and call SQL whose contract
this epic owns. Story 6.4 documents the control queries; Story 6.9
documents the stats query.

---

## gRPC services owned

**None.** Queue operations are Python-side (Pipeline) or Go-side SQL
(API). Live progress streaming uses `LISTEN/NOTIFY` (`jobs.progress`),
not gRPC.

---

## LISTEN/NOTIFY channels owned

The full canonical `jobs.*` namespace (resolves REVIEW §2.3.a — singular
names like `job.pending` are retired):

| Channel | Producer | Consumer | Story |
|---------|----------|----------|-------|
| `jobs.new` | enqueue | claim loop | 6.1, 6.2 |
| `jobs.flag_set` | API control | worker | 6.4 |
| `jobs.progress` | `tick_progress` | API → WS | 6.3 |
| `jobs.heartbeat` | `tick_heartbeat` | reaper only | 6.3 |
| `jobs.reaped` | reaper UPDATE | API → WS | 6.6 |
| `jobs.force_pause` | API force-pause | worker abort listener | 6.4 |

---

## Dependencies

**Depends on.**
- Epic 01 Story 1.6 — the canonical stage enum and the video state
  machine the queue references. That's it.

**Depended on by.** **Every other pipeline epic.** Specifically:
- Epic 01 (Stories 1.1–1.5) — `enqueue_scan`, `enqueue_probe`.
- Epic 02 — claim/heartbeat/cap on `probe` and `extract`.
- Epic 03 — claim, heartbeat, request flags, retry, reaper, caps on
  `transcribe`.
- Epic 04 — same for `subtitle_gen`.
- Epic 05 — same for `index`.
- Epic 07 (API) — pause/resume/cancel REST endpoints + `jobs.progress`
  WebSocket fanout + `/api/queue/stats`.

---

## Key technical decisions

- **Postgres-native queue.** `SELECT … FOR UPDATE SKIP LOCKED` is the
  claim primitive; transactions are the locking mechanism. No external
  broker.
- **5-second heartbeat, 90-second reaper threshold.** `90 = 18 × 5`
  (REVIEW §1.4.c — the previously documented 30 s value was wrong;
  Story 6.6's comment is corrected). Reaper wakes every 30 s and flips
  stale rows to `paused`, clearing `claimed_by`.
- **Backoff with jitter.** First retry ~60 s, doubling, capped at
  3600 s. ±25 % jitter to avoid thundering herd. `max_attempts` defaults
  to 3.
- **Request flags, not state mutations.** `pause_requested` and
  `cancel_requested` are Boolean flags that workers poll cooperatively
  per segment. The worker self-transitions. Force-pause is the
  exception (API flips state directly + notifies on
  `jobs.force_pause`).
- **Per-host concurrency caps.** Stage-level semaphores: `scan=4`,
  `probe=4`, `extract=2`, `transcribe=GPU-count`, `subtitle_gen=2`,
  `index=4`, `thumbnail=2`. GPU devices are pinned per-`transcribe` for
  exclusivity.
- **Graceful shutdown.** `SIGTERM` sets a shutdown event; the
  orchestrator flips `pause_requested=true` on every in-flight claim,
  waits up to 120 s for cooperative pauses, then force-pauses
  stragglers. A second `SIGTERM` calls `os._exit(130)`; the reaper
  cleans up within 90 s.
- **Single source of truth for resume.** Story 6.10 enforces:
  `processing_jobs.last_segment_end_sec` is the *only* resume offset.
  Sidecar files are linted out; a schema CHECK enforces
  monotonicity. Segment commits update this column in the same
  transaction as the segment INSERT.
- **Observability.** Structured logs on every state change; Prometheus
  counters (`maktaba_job_attempts_total`, duration histogram, RTF
  summary). `/api/queue/stats` aggregates by `stage × state` with ETA
  and p50 RTF. Metrics exposed on Pipeline `:9101` and the API's
  `/metrics`.

---

## Libraries / dependencies introduced

| Library | Used by | Purpose |
|---------|---------|---------|
| `asyncpg` | Pipeline | Async Postgres driver + LISTEN/NOTIFY |
| `aiosqlite` | Pipeline tests | SQLite async driver for fast tests |
| `prometheus_client ≥ 0.20` | Pipeline | Metric emission |
| `structlog` | Pipeline | JSON-structured logs |
| `pynvml ≥ 11.5` (optional) | Pipeline | NVIDIA GPU enumeration for caps |
| `prometheus/client_golang/prometheus` | API | Counters / histograms |
| `go-chi/chi v5` | API | HTTP router for control + stats endpoints |

No new internal deps; everything is stdlib or already-pinned.

---

## Test coverage summary

- **Unit:** SQL claim predicate, reaper math, retry/backoff curve, flag
  observation, state transitions.
- **Integration:** claim → run → heartbeat → finish round-trips;
  pause/resume/cancel; reaper flips stale rows.
- **Property-based (Hypothesis):** the resume invariant — 100 chaos
  cycles of crash+resume; `last_segment_end_sec ≡ max(transcript
  segment end_sec)` always holds.
- **Lint:** no sidecar checkpoint files, no `*_resume_offset` columns,
  no singular `job.*` channel names.
- **Performance:** per-tick overhead — claim ~1 ms, reaper no-op
  ~0.5 ms, progress update ~0.3 ms.

Pinned invariants:
- No double-claim (the `SELECT FOR UPDATE SKIP LOCKED` predicate plus a
  partial unique index).
- Exactly-once enqueue (idempotency guard on `(video_id, stage)`).
- Idempotent retry + flag observation.
- No orphaned claims after the reaper (state always flips to `paused`,
  `claimed_by` cleared).
- Single source of truth for resume (linted + checked + property-tested).
