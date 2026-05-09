# Epic 06 — Job Queue

**Goal.** Implement the durable, atomic, pause-aware job queue that every
other pipeline stage rides on. The queue is a single Postgres table
(`processing_jobs`, schema in `architecture §7.1`), not a broker, not a
Celery, not a Redis. All claim, heartbeat, retry, pause, resume, cancel,
and reaper logic is here, and is the single concern of this epic — the
stages above just call into it.

**Owner.** Pipeline Service,
`pipeline/src/maktaba_pipeline/pipeline/runner.py` (claim loop, worker)
and `pipeline/src/maktaba_pipeline/db/jobs.py` (SQL).

**Out of scope.** Per-stage implementations (Epics 1–5); UI rendering of
job state (`web/src/pages/queue.tsx`, separate doc); the API endpoints
themselves (`/api/jobs/*`, owned by
[`02-api-streaming.md`](../../02-api-streaming.md)).

## Standardized notify channel naming

> **Resolves REVIEW §2.3.a.** The original pipeline epic mixed
> `job.pending` (singular) and `jobs.new` (plural) for the same
> channel. This epic standardizes on the **plural `jobs.*` namespace**
> for all job-related channels and **plural domain namespaces**
> (`videos.*`, `segments.*`, `library.*`, `profiles.*`, `settings.*`,
> `jwks.*`) for non-job channels. The full canonical channel set
> consumed and produced by the pipeline:

| Channel | Producer | Consumer | Owner story |
|---------|----------|----------|-------------|
| `videos.new` | scanner | API → WS | [Epic 1 Story 1.1](../01-scanner/story-01-01-file-discovery.md) |
| `videos.state_changed` | reprocess | API | [Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md) |
| `jobs.new` | enqueue | claim loop | [Story 6.1](story-06-01-schema-indexes.md), [Story 6.2](story-06-02-claim-loop.md) |
| `jobs.flag_set` | API | worker | [Story 6.4](story-06-04-pause-resume-cancel.md) |
| `jobs.progress` | worker | API → WS | [Story 6.3](story-06-03-heartbeat-progress.md) |
| `jobs.heartbeat` | worker | reaper | [Story 6.3](story-06-03-heartbeat-progress.md) |
| `jobs.reaped` | reaper | API → WS | [Story 6.6](story-06-06-reaper.md) |
| `jobs.force_pause` | API | worker | [Story 6.4](story-06-04-pause-resume-cancel.md) |
| `segments.committed` | transcribe | live indexer | [Epic 3 Story 3.6](../03-transcription/story-03-06-segment-commit.md) |

The previously-used `job.pending`, `job.progress`, `job.heartbeat`,
`job.reaped`, `job.force_pause` (singular) names are retired.

## Stories

| # | Status | Title | File |
|---|--------|-------|------|
| 6.1 | ✅ landed | Schema, migration, indexes | [story-06-01-schema-indexes.md](story-06-01-schema-indexes.md) |
| 6.2 | | Claim loop | [story-06-02-claim-loop.md](story-06-02-claim-loop.md) |
| 6.3 | | Heartbeat & progress (with `jobs.*` channels) | [story-06-03-heartbeat-progress.md](story-06-03-heartbeat-progress.md) |
| 6.4 | | Pause, resume, cancel via request flags | [story-06-04-pause-resume-cancel.md](story-06-04-pause-resume-cancel.md) |
| 6.5 | | Backoff and retry | [story-06-05-backoff-retry.md](story-06-05-backoff-retry.md) |
| 6.6 | | Reaper for crashed claims (heartbeat math fixed) | [story-06-06-reaper.md](story-06-06-reaper.md) |
| 6.7 | | Concurrency model & per-host caps | [story-06-07-concurrency-caps.md](story-06-07-concurrency-caps.md) |
| 6.8 | | Graceful shutdown semantics | [story-06-08-graceful-shutdown.md](story-06-08-graceful-shutdown.md) |
| 6.9 | | Observability hooks | [story-06-09-observability.md](story-06-09-observability.md) |
| 6.10 | | Single source of truth for resume | [story-06-10-resume-invariant.md](story-06-10-resume-invariant.md) |

## Resolved cross-doc issues

- **REVIEW §1.3.c** (`thumb` vs `thumbnail`). The canonical stage name
  is `thumbnail`; see
  [Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md).
- **REVIEW §1.4.c** (heartbeat 5 s vs 30 s). The heartbeat is **5 s**;
  Story 6.6's comment is corrected to `90 s = 18 × 5 s heartbeat`.
- **REVIEW §2.3.a** (channel naming). Resolved above.

## Dependency notes

- 6.1–6.3 unblock the full pipeline.
- Every other epic depends on this one; nothing in this epic depends on
  another (modulo the schema definition for video states owned by
  [Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md)).
