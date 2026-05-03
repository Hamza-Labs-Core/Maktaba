# Story 3.5 — Backend registry, transcript history & per-library selection

## Description

The transcribe stage at run time picks the backend declared by the
library's settings, with a fallback chain if the chosen backend is
unhealthy. Re-running transcription with a different backend, model, or
configuration must preserve the previous transcript so that diffs and
audits are possible — this story owns the schema change that makes
multi-version transcripts safe to keep.

> **Resolves REVIEW §1.1.b and §1.1.i.** The `transcripts` UNIQUE
> constraint as defined in `architecture.md §8.1`
> (`UNIQUE (video_id, audio_track_id, backend, model)`) blocks
> reprocessing with the same `(backend, model)` combination, and the
> architecture schema does not contain `is_active`, even though Stories
> 4.3, 4.5, 5.1, 5.5 of the original pipeline epic all consume it. This
> story is the **single owner** of both fixes: it adds the column and
> rewrites the constraint to be partial.

## Acceptance criteria

- `pipeline.stt.registry.list()` returns every backend whose
  `health.ready == true` at the moment of the call.
- Per-library config has `stt.backend = "<name>"` (architecture §11.4).
  At job time, the stage looks up the backend, runs its preflight, and
  if `ready=false`, walks `stt.fallback = ["whisper-cuda",
  "whisper-cpu"]` until one is ready or no fallback remains; if none,
  the job fails with `error.kind = "no_backend_ready"`.
- The chosen `(backend, model)` is persisted on the `transcripts` row
  (`backend`, `model`, `backend_version`); re-running a transcribe with
  a different backend, a different model, or *the same* `(backend, model)`
  creates a **new** transcript row; the old one is preserved for
  diff/comparison and tagged with `transcripts.is_active = false`.
- A migration `shared/db/migrations/000X_transcripts_is_active.sql`:
  1. Adds `is_active BOOLEAN NOT NULL DEFAULT TRUE` to `transcripts`.
  2. Drops the existing constraint
     `UNIQUE (video_id, audio_track_id, backend, model)`.
  3. Replaces it with the partial unique index
     `CREATE UNIQUE INDEX transcripts_active_unique
        ON transcripts (video_id, audio_track_id)
        WHERE is_active = true;`
     so that **at most one active transcript exists per
     `(video_id, audio_track_id)`** while history rows are unconstrained.
  4. Backfills `is_active = true` for the latest `created_at` per
     `(video_id, audio_track_id)` and `false` for the rest, in a single
     transaction.
- The orchestrator's "flip active" path uses one transaction:
  `UPDATE transcripts SET is_active = false WHERE video_id = $v AND
  audio_track_id = $t AND is_active = true;
   INSERT INTO transcripts (..., is_active) VALUES (..., true);`
  with the partial-unique constraint enforcing correctness even under
  concurrent flips.

## Test cases

- `test_registry_filters_unhealthy` — patch one backend's `health.ready`
  to false → `list()` excludes it.
- `test_fallback_walks_chain` — primary backend reports unhealthy →
  the next ready one is used; recorded in `metrics.fallback_from`.
- `test_reprocess_creates_new_transcript_row` — re-running with a
  different `model` → new row with `is_active = true`; old row's
  `is_active` flips to false in the same transaction.
- `test_reprocess_with_same_backend_and_model_works` — re-running with
  the **same** `(backend, model)` → succeeds (new row created, old
  flipped); previously failed before this story's migration.
- `test_partial_unique_blocks_double_active` — two concurrent flips
  attempting to activate two new transcripts for the same
  `(video_id, audio_track_id)` → exactly one wins; the other raises a
  unique-violation that the orchestrator retries.
- `test_migration_backfills_is_active` — apply migration on a fixture DB
  with two pre-existing rows for one `(video_id, audio_track_id)` →
  exactly one row has `is_active = true`, namely the most recent.

## Edge cases

- **All backends unhealthy at claim time.** The job is requeued with
  `not_before = now() + 60s` (rather than failed) up to
  `max_attempts`, then failed.
- **A backend listed in `fallback` is missing from the build.** Treated
  as `health.ready=false`; logged once at startup.
- **Reader sees momentary "no active transcript".** Within the flip
  transaction, both UPDATE and INSERT commit atomically; outside the
  transaction, readers see either the old active row or the new active
  row — never neither, never both.
- **Subtitle generation depends on the active transcript.** Epic 4
  Story 4.5's `transcript_segments_v` view filters on `is_active = true`;
  flipping active triggers a new `subtitle_gen` enqueue for the new
  transcript and an invalidation pass for the previous transcript's
  artifacts (Epic 4 Story 4.1 retry path).
