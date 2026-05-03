# Story 19.4 — Horizontal scale-out for the pipeline service

Multiple pipeline workers across hosts coordinate via the Postgres job
queue; GPU stages take per-device locks; CPU stages have per-host caps.

ChromaDB upserts remain bounded by the documented single-writer rule
(see [Story 24.4](../24-data-integrity/story-24-04-concurrency-locking.md)
and architecture §10.3): in v1 the embedded ChromaDB store accepts only
one writer at a time. Horizontal pipeline scale-out is therefore valid for
all stages *except* concurrent ChromaDB writes from multiple hosts; the
multi-writer path is reserved for the ChromaDB-server deployment, which
is deferred. Workers detect a peer writer at startup and the second
worker logs and refuses to write.

## Acceptance criteria

- AC1. N workers (N ≥ 2) on distinct hosts, all pointed at the same
  Postgres and the same shared media volume, drain a 100-job queue in
  ≤ T/N + ε wall-clock (where T is single-host time), provided the queue
  contains at most one ChromaDB-writing job in flight at any time.
- AC2. `SELECT … FOR UPDATE SKIP LOCKED` ensures every job runs exactly
  once across all workers (verified by output uniqueness on the
  fixture).
- AC3. GPU-bound jobs claim a per-device advisory lock keyed by
  `(host_id, device_id)`; two GPU jobs on the same device serialize.
- AC4. Adding a worker host requires only a config file pointing at the
  shared DB and media volume; no code change.

## Test cases

- TC1. Two-host drain: enqueue 60 minutes of audio across 30 jobs; two
  workers (each with 1 GPU) finish in ≈ half the single-host time.
- TC2. Exactly-once: with 4 workers and 1,000 small jobs, the output
  table contains 1,000 unique (job_id, output_hash) rows.
- TC3. Lock contention: pin two GPU jobs to the same device; their
  effective wall-clock is sequential, not parallel.
- TC4. Single-writer guard: launch two pipeline workers against the
  same embedded ChromaDB; the second worker exits the writer role with
  a clear log line and the first continues.

## Edge cases

- EC1. A worker dies mid-job — the heartbeat reaper returns the job to
  `pending` after `heartbeat_sec × 3`; another worker re-claims it and
  resumes from `last_segment_end_sec`.
- EC2. Shared media volume is unreliable (NFS hiccup) — read errors are
  retried with exponential backoff; the job is requeued, not failed,
  after 3 attempts.
- EC3. Workers running mismatched code versions — each job records the
  worker's `version`, `backend`, `model_hash`; a mismatch with the
  library's expected `(backend, model)` fails the job into a
  human-readable retry pile.
