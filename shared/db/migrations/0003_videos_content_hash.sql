-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0003 (Story 1.2 / plan-01-02) — content-addressable identity.
--
-- Replaces the global UNIQUE on `videos.content_hash` (introduced in
-- slot 0001) with the per-library UNIQUE the story spec mandates:
-- two files with identical bytes share one row *within a library* but
-- get independent rows across libraries (so deleting a library never
-- orphans cross-library data — see story-01-02 §"Uniqueness scope").
--
-- Adds:
--   * `videos_library_content_hash_key` UNIQUE INDEX (library_id, content_hash)
--   * `videos_content_hash_lookup_idx`  helper for content-hash lookups
--   * `videos_content_hash_format_chk`  CHECK pinning the 64-lower-hex shape
--   * `videos_additional_paths_gin_idx` GIN over `metadata->'additional_paths'`
--
-- The `metadata` JSONB column is owned by slot 0001; this migration
-- only indexes the `additional_paths` array within it. The scanner
-- (Story 1.1) appends to that array when it discovers a duplicate
-- file in the same library.
--
-- NO TRANSACTION because Postgres `CREATE INDEX CONCURRENTLY` cannot
-- run inside a transaction. Each statement is individually idempotent
-- (IF [NOT] EXISTS) so a partially-applied migration is safe to re-run.
--
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_content_hash_key;
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-library uniqueness — the canonical identity for a (library, content) pair.
-- A unique index is identical to a UNIQUE constraint for the scanner's
-- `INSERT … ON CONFLICT (library_id, content_hash)` upsert path; we keep
-- it as an index so the build can use CONCURRENTLY.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS videos_library_content_hash_key
    ON videos (library_id, content_hash);
-- +goose StatementEnd

-- +goose StatementBegin
-- "Find me every row in this library by content hash" — covers the
-- duplicate-detection path on insert and the cross-library audit query.
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_content_hash_lookup_idx
    ON videos (content_hash);
-- +goose StatementEnd

-- +goose StatementBegin
-- 64 lowercase hex chars. The Python hasher (`maktaba_pipeline.identity`)
-- only emits this shape; the constraint guards against buggy direct
-- INSERTs sneaking malformed values past application validation.
-- Wrapped in DO/IF NOT EXISTS because Postgres has no
-- `ALTER TABLE … ADD CONSTRAINT … IF NOT EXISTS`.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'videos_content_hash_format_chk'
    ) THEN
        ALTER TABLE videos
            ADD CONSTRAINT videos_content_hash_format_chk
            CHECK (content_hash ~ '^[0-9a-f]{64}$') NOT VALID;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Validate after the constraint exists — at slot 0003 application time
-- the table is empty in any practical scenario, so this is a no-op
-- scan in CI. NOT VALID + VALIDATE pattern keeps us safe if a backfill
-- ever lands these rows ahead of the constraint.
ALTER TABLE videos VALIDATE CONSTRAINT videos_content_hash_format_chk;
-- +goose StatementEnd

-- +goose StatementBegin
-- GIN over `metadata->'additional_paths'` so "which row owns this
-- path?" lookups stay fast even after rename round-trips. The `?`
-- containment operator on a JSONB array uses this index.
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_additional_paths_gin_idx
    ON videos USING GIN ((metadata -> 'additional_paths'));
-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_additional_paths_gin_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP CONSTRAINT IF EXISTS videos_content_hash_format_chk;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS videos_content_hash_lookup_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS videos_library_content_hash_key;
-- +goose StatementEnd

-- +goose StatementBegin
-- Restore the global UNIQUE from slot 0001 so down-migration is a
-- proper inverse. Cannot use CONCURRENTLY inside a NO TRANSACTION
-- migration when the column is constrained NOT NULL — the lock is
-- already held during the ALTER.
ALTER TABLE videos
    ADD CONSTRAINT videos_content_hash_key UNIQUE (content_hash);
-- +goose StatementEnd
