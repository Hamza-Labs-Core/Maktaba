# Story 6.1 — Schema, migration, indexes

## Description

The schema in architecture §7.1 lands as one migration with all the
indexes needed for the claim, reaper, and progress queries.

## Acceptance criteria

- Migration `shared/db/migrations/000X_jobs.sql` creates
  `processing_jobs` exactly as specified in architecture §7.1, including
  all four indexes:
  - `(state, priority, not_before)` — the claim index.
  - `(video_id, stage)` — for "what's pending for this video".
  - `(state, last_heartbeat_at) WHERE state IN ('claimed', 'running',
    'resuming')` — the reaper's partial index.
  - `(pause_requested) WHERE pause_requested = true` — the pause poller.
- The `stage` column has a CHECK constraint matching the canonical
  enum from
  [Epic 1 Story 1.6](../01-scanner/story-01-06-video-state-machine.md):
  `CHECK (stage IN ('scan','probe','extract','transcribe',
  'subtitle_gen','index','thumbnail'))`.
- The same migration runs on Postgres and on SQLite (with the noted
  type swaps in architecture §8.0 preamble).
- An `enqueue(video_id, stage, priority, payload?)` Python helper writes
  one row, sets `state = 'pending'`, `attempts = 0`,
  `not_before = NULL`, returns the new id. Idempotency: if a row with
  the same `(video_id, stage)` already exists in a non-terminal state,
  return its id without inserting (or, if the stage is meant to be
  re-runnable like `index`, skip when state is `done` and the source's
  `updated_at <= last run finished_at`).
- After a successful `enqueue`, the helper emits `NOTIFY jobs.new` with
  payload `{id, video_id, stage, priority}`. (Channel name standardized
  in [README](README.md).)

## Test cases

- `test_migration_creates_indexes` — query `pg_indexes` (or
  `sqlite_master`) → all four indexes present.
- `test_stage_check_constraint` — INSERT with `stage='thumb'` fails;
  with `stage='thumbnail'` succeeds; with `stage='subtitle_gen'`
  succeeds.
- `test_enqueue_idempotent` — call `enqueue(v, 'probe')` twice → only
  one row.
- `test_enqueue_skips_when_done_and_source_unchanged` — a `done` row
  with finished_at > video.updated_at → enqueue is a no-op.
- `test_enqueue_creates_new_when_source_changed` — bump
  `videos.updated_at` past `finished_at` → enqueue inserts a fresh
  pending row.
- `test_enqueue_emits_jobs_new_notify` — listen on `jobs.new`; assert
  one notification per successful insert with the documented payload.

## Edge cases

- **Concurrent enqueue.** Two callers race on the same `(video_id,
  stage)`. The unique partial index `UNIQUE (video_id, stage) WHERE
  state IN ('pending','claimed','running','resuming','paused')`
  guarantees at most one non-terminal row per pair; the loser's INSERT
  raises and is converted to "row already pending".
- **SQLite single-writer.** The enqueue path uses a brief `BEGIN
  IMMEDIATE` to serialize writes; readers continue under WAL.
