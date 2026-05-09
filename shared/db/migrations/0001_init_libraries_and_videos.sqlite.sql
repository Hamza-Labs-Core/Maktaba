-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0001.
--
-- Substitutions (per migrations/README.md §3 and architecture.md §8):
--   UUID         → TEXT (caller is responsible for generating UUIDs)
--   JSONB        → JSON
--   TIMESTAMPTZ  → TEXT (ISO-8601, e.g. '2026-05-09T12:34:56Z')
--   TEXT[]       → TEXT (a JSON-encoded array string; see roots column)
--
-- SQLite has no CONCURRENTLY, no partial indexes on TIMESTAMPTZ
-- predicates with `now()`-relative cutoffs, and no `gen_random_uuid()`.
-- Application code generates UUIDs and ISO-8601 timestamps before
-- INSERT. The lint exempts .sqlite.sql files from the long-running
-- check, so plain `CREATE INDEX IF NOT EXISTS` is correct here.
--
CREATE TABLE IF NOT EXISTS libraries (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL UNIQUE,
    roots       TEXT    NOT NULL,
    settings    TEXT    NOT NULL DEFAULT '{}',
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS videos (
    id                 TEXT     PRIMARY KEY,
    library_id         TEXT     NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    content_hash       TEXT     NOT NULL UNIQUE,
    path               TEXT     NOT NULL,
    filename           TEXT     NOT NULL,
    size_bytes         INTEGER  NOT NULL CHECK (size_bytes >= 0),
    mtime              TEXT     NOT NULL,
    state              TEXT     NOT NULL DEFAULT 'discovered',
    detected_language  TEXT,
    title              TEXT,
    description        TEXT,
    poster_path        TEXT,
    sprite_path        TEXT,
    duration_sec       REAL,
    metadata           TEXT     NOT NULL DEFAULT '{}',
    created_at         TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at         TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_library_state_idx
    ON videos (library_id, state);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_library_path_idx
    ON videos (library_id, path);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_detected_language_idx
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
