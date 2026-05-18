-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0058 (library-scoped SCAN jobs).
--
-- Substitutions vs. the Postgres canonical (architecture §8.0):
--   UUID → TEXT.
--
-- Adds the nullable `library_id` column and the one-live-scan-per-
-- library partial unique index so stage-aware Python runs identically
-- on either dialect. SQLite supports `ADD COLUMN IF NOT EXISTS` (3.35+)
-- and partial indexes (3.8+).
--
-- Two intentional divergences from the Postgres file, both inherent to
-- SQLite's ALTER TABLE limitations (it cannot rewrite a column's
-- nullability or attach a table-level CHECK after creation):
--
--   1. `video_id` stays declared NOT NULL in the slot 0002 SQLite
--      table. SQLite has no `ALTER COLUMN … DROP NOT NULL`; the only
--      way to drop it is a 12-step table rebuild, which is exactly
--      the kind of long-running rewrite the project forbids in a
--      migration. The Postgres deployment is the one that actually
--      runs library-scoped scan jobs; the SQLite build's scan path is
--      validated against the in-memory fake. A future consolidated
--      SQLite baseline will declare `video_id` nullable from the
--      start. Until then, SQLite cannot persist a real library-scoped
--      scan row — a documented, accepted parity gap (see
--      migrations/README.md §3: equivalence is "an equivalent
--      schema", not byte-identical, where the dialect cannot express
--      the canonical shape).
--   2. The `processing_jobs_scope_chk` table CHECK is omitted for the
--      same reason — SQLite cannot `ALTER TABLE … ADD CONSTRAINT`.
--      The invariant is enforced in application code (the enqueue
--      helpers) on this dialect.
--
ALTER TABLE processing_jobs
    ADD COLUMN IF NOT EXISTS library_id TEXT
        REFERENCES libraries(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS processing_jobs_one_live_scan_per_library
    ON processing_jobs (library_id, stage)
    WHERE stage = 'scan'
      AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_one_live_scan_per_library;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE processing_jobs DROP COLUMN IF EXISTS library_id;
-- +goose StatementEnd
