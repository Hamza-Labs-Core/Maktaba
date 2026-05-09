-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0001 (Story 1.5 / plan-01-05) — foundational schema.
--
-- Creates the two base tables every other epic depends on: `libraries`
-- (one row per logical collection) and `videos` (one row per identified
-- file *per library*, see story-01-05 §"uniqueness key").
--
-- Scope of THIS migration:
--   - `libraries` with a transitional `roots TEXT[]` column. The
--     canonical normalized store is `library_roots`, owned by
--     plan-09-16; pre-09-16 plans (the Scanner epic) read/write the
--     transitional array column on this table.
--   - `videos` with `metadata JSONB` extension column shared across
--     plans 01-02 (additional_paths), 01-03 (missing_since), 01-04
--     (deleted_at), and 02-02 (track_override) — see
--     architecture.md §8.7.
--   - `content_hash` carries a GLOBAL UNIQUE here. Slot 0003
--     (plan-01-02) drops it and replaces with
--     `UNIQUE (library_id, content_hash)`; that's the per-library
--     uniqueness rule story-01-05 ratifies.
--   - `state` is plain TEXT defaulting to 'discovered'. The 12-state
--     CHECK constraint is owned by slot 0004 (plan-01-06).
--
-- Out of scope (other slots own these):
--   - `processing_jobs`              → slot 0002 (plan-06-01)
--   - per-library `(library_id, content_hash)` UNIQUE → slot 0003 (plan-01-02)
--   - `videos.state` CHECK + stage enum → slot 0004 (plan-01-06)
--   - `videos.new` NOTIFY trigger    → slot 0005 (plan-01-01)
--   - `library_scan_state`, `purge_log` → slot 0006 (this plan)
--   - `videos.last_seen_at`, `videos.deleted_at` → slot 0007 (this plan)
--
-- Why NO TRANSACTION: `CREATE INDEX CONCURRENTLY` (required by the
-- migration-lint long-running rule for Postgres) cannot run inside an
-- explicit transaction. Each statement is individually idempotent
-- (IF NOT EXISTS), so a half-applied migration is safe to re-run.
--
CREATE EXTENSION IF NOT EXISTS pgcrypto;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS libraries (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT         NOT NULL UNIQUE,
    roots       TEXT[]       NOT NULL,
    settings    JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS videos (
    id                 UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id         UUID         NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    content_hash       TEXT         NOT NULL UNIQUE,
    path               TEXT         NOT NULL,
    filename           TEXT         NOT NULL,
    size_bytes         BIGINT       NOT NULL CHECK (size_bytes >= 0),
    mtime              TIMESTAMPTZ  NOT NULL,
    state              TEXT         NOT NULL DEFAULT 'discovered',
    detected_language  TEXT,
    title              TEXT,
    description        TEXT,
    poster_path        TEXT,
    sprite_path        TEXT,
    duration_sec       REAL,
    metadata           JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at         TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_state_idx
    ON videos (library_id, state);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_path_idx
    ON videos (library_id, path);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_detected_language_idx
    ON videos (detected_language)
    WHERE detected_language IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_detected_language_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS videos_library_path_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS videos_library_state_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS libraries;
-- +goose StatementEnd
