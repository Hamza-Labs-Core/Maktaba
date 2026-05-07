# Story 1.5 — Schema & ownership decisions

## Description

This story is a one-time decision needed before the scanner ships, captured
here so it does not get hidden in code:

- **Uniqueness key.** `videos.content_hash` is `UNIQUE` per library, not
  globally. This permits the same source file to be ingested into multiple
  libraries with different settings (e.g., a tutorial in both `Lectures`
  and `Films`), at the cost of duplicate transcription work if the user
  does so. We accept that trade-off because cross-library de-duplication
  would require a join on the search side and complicates the "delete a
  library" semantics. (See [Story 1.2](story-01-02-content-identity.md)
  for the consumer-side contract that depends on this decision.)
- **Soft delete.** Files removed from disk become `state = MISSING` and
  retain all derived data. A `--purge-missing` flag on the scanner CLI
  hard-deletes any video that has been `MISSING` for ≥ 7 days. The full
  state machine is defined in [Story 1.6](story-01-06-video-state-machine.md).
- **`.maktaba/` sidecar directory.** Created lazily on first generated
  artifact; `chmod 755`; not synced to derived data caches.

## Acceptance criteria

- The migration file `shared/db/migrations/000X_videos_unique_per_library.sql`
  drops the global `UNIQUE (content_hash)` constraint defined in
  `architecture.md §8.1` and replaces it with
  `UNIQUE (library_id, content_hash)`. The migration is applied as part
  of the scanner ship and is the **single owner** of this schema change
  for the pipeline epic.
- The state machine in `pipeline/src/maktaba_pipeline/domain/states.py`
  includes `MISSING` as a non-terminal sink with one allowed transition
  back to `DISCOVERED` on rediscovery. (Definition in
  [Story 1.6](story-01-06-video-state-machine.md).)
- A `--purge-missing` CLI flag exists, defaults to off, and prompts before
  deleting unless `--yes` is passed.

## Test cases

- `test_migration_drops_global_unique` — apply the migration on a fixture
  DB seeded under the old constraint; the constraint
  `UNIQUE (content_hash)` is gone; `UNIQUE (library_id, content_hash)`
  is present (verified by `pg_indexes` / `sqlite_master`).
- `test_migration_idempotent` — applying the migration twice succeeds
  without error.
- `test_purge_missing_requires_age` — a video that has been `MISSING` for
  3 days is **not** purged; one that has been `MISSING` for 8 days is.
- `test_purge_missing_prompts_without_yes` — running `--purge-missing`
  interactively without `--yes` aborts on stdin EOF.

## Edge cases

- **Migration on a DB that already contains cross-library duplicates.**
  Cannot happen under the original schema because the global UNIQUE
  prevented them; the migration is a constraint relaxation, not a data
  rewrite. Recorded as a precondition in the migration's docstring.
- **Pipeline running during migration.** The migration takes a brief
  ACCESS EXCLUSIVE lock on `videos`; in production, run during the
  documented quiet window (Epic 22 deploy story).
