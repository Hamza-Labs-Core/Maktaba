-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0058 (gap-closure HLB-257/255) — decouple library-scoped jobs
-- from the per-video `processing_jobs.video_id NOT NULL` invariant so
-- the real SCAN stage can enqueue one job per *library* (no `videos`
-- row exists at scan time — the scan is what discovers them).
--
-- Additive, append-only. Slot 0002 owns the canonical
-- `processing_jobs` table; this slot only ALTERs it:
--
--   * ADD `library_id UUID NULL REFERENCES libraries(id) ON DELETE
--     CASCADE` — deleting a library reaps its in-flight scan job the
--     same way deleting a video reaps its per-video jobs.
--   * DROP NOT NULL on `video_id`. This is a catalog-only flip
--     (`pg_attribute.attnotnull = false`) — no table rewrite, no
--     scan — so it does not trip the migration-lint long-running
--     `SET NOT NULL` rule (that rule targets the *opposite*
--     direction, which does scan the table).
--   * ADD partial UNIQUE `processing_jobs_one_live_scan_per_library`
--     on `(library_id, stage)` for scan jobs in the same five live
--     states the per-video `processing_jobs_one_live_per_video_stage`
--     index uses. One concurrent scan per library, enforced the same
--     race-free way `enqueue` relies on for per-video stages.
--   * ADD `processing_jobs_scope_chk`: a scan job carries `library_id`
--     and null `video_id`; every other stage carries `video_id`. This
--     keeps the existing per-video unique index meaningful for
--     PROBE/EXTRACT/TRANSCRIBE (their rows always have `video_id`).
--
-- The slot 0002 `processing_jobs_one_live_per_video_stage` index is
-- left untouched: PROBE/EXTRACT/TRANSCRIBE idempotency still rides on
-- it. Its predicate `WHERE state IN (…)` does not filter on
-- `video_id IS NOT NULL`, but the scope CHECK guarantees scan rows
-- have null `video_id`, and a unique index treats null as distinct, so
-- scan rows never collide on the per-video index regardless.
--
-- This file disables goose's per-migration transaction wrapper (the
-- directive on line 1) because Postgres CREATE INDEX CONCURRENTLY
-- cannot run inside an explicit transaction. Every statement is
-- individually idempotent so a half-applied migration is safe to
-- re-run.
--
ALTER TABLE processing_jobs
    ADD COLUMN IF NOT EXISTS library_id UUID
        REFERENCES libraries(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
-- Metadata-only: clears pg_attribute.attnotnull, no table scan.
ALTER TABLE processing_jobs
    ALTER COLUMN video_id DROP NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
-- Scope invariant: scan jobs are library-scoped (library_id set,
-- video_id null); every other stage is per-video (video_id set).
-- Wrapped in DO/IF NOT EXISTS because Postgres has no
-- `ALTER TABLE … ADD CONSTRAINT … IF NOT EXISTS` (same pattern as
-- slot 0003's content-hash-format check). NOT VALID + VALIDATE keeps
-- a future backfill safe; at slot-apply time the table is empty so
-- the validate is a no-op scan.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'processing_jobs_scope_chk'
    ) THEN
        ALTER TABLE processing_jobs
            ADD CONSTRAINT processing_jobs_scope_chk
            CHECK (
                (stage =  'scan' AND library_id IS NOT NULL AND video_id IS NULL)
             OR (stage <> 'scan' AND video_id IS NOT NULL)
            ) NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs VALIDATE CONSTRAINT processing_jobs_scope_chk;
-- +goose StatementEnd

-- +goose StatementBegin
-- One live scan per library. Same five live states as the per-video
-- unique index from slot 0002; restricted to scan rows so it never
-- contends with per-video stages.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_one_live_scan_per_library
    ON processing_jobs (library_id, stage)
    WHERE stage = 'scan'
      AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_one_live_scan_per_library;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP CONSTRAINT IF EXISTS processing_jobs_scope_chk;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP COLUMN IF EXISTS library_id;
-- +goose StatementEnd

-- Down deliberately stops here. It does NOT re-assert NOT NULL on
-- video_id: re-adding that constraint requires a whole-table scan
-- (the migration-lint long-running rule forbids the catalog-level
-- re-add precisely because it is not metadata-only in that
-- direction). Per migrations/README.md §6 down blocks are dev-only
-- and need not be a byte-perfect inverse; leaving video_id nullable
-- after a rollback is strictly more permissive than slot 0002 and
-- harmless — every non-scan write path still supplies video_id and
-- the per-video unique index is untouched.
