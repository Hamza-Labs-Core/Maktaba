-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0003 (Story 1.2 / plan-01-02).
--
-- The Postgres canonical drops the global UNIQUE on `content_hash` and
-- replaces it with `UNIQUE (library_id, content_hash)`. SQLite has no
-- `ALTER TABLE DROP CONSTRAINT`, so the column-level UNIQUE inherited
-- from slot 0001 cannot be removed in place — we follow the standard
-- "12-step" procedure: build a new table, copy data, swap names.
--
-- The rebuilt schema mirrors slot 0001's SQLite shape exactly except:
--   * column-level `UNIQUE` on `content_hash` is dropped
--   * a table-level `UNIQUE (library_id, content_hash)` takes its place
--   * a CHECK constraint pins the 64-lower-hex format
--
-- Slots 0006/0007 add columns later (`last_seen_at`, `deleted_at`); we
-- intentionally do NOT include them here — they apply on top of the
-- rebuilt table when those slots run.
--
-- `defer_foreign_keys=ON` lets the rebuild swap tables atomically
-- without tripping the FK from any future child rows. Per the SQLite
-- docs (https://www.sqlite.org/lang_altertable.html#otheralter), this
-- is the canonical pattern.
--
-- A leftover from a partial previous run is dropped first so re-runs
-- are idempotent. The lookup index is added at the end as a small
-- helper (parity with the Postgres `videos_content_hash_lookup_idx`).
--
DROP TABLE IF EXISTS _videos_03_rebuild;
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA defer_foreign_keys = ON;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS _videos_03_rebuild (
    id                 TEXT     PRIMARY KEY,
    library_id         TEXT     NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    content_hash       TEXT     NOT NULL,
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
    updated_at         TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (library_id, content_hash),
    CHECK (
        length(content_hash) = 64
        AND content_hash NOT GLOB '*[^0-9a-f]*'
    )
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO _videos_03_rebuild (
    id, library_id, content_hash, path, filename, size_bytes, mtime,
    state, detected_language, title, description, poster_path,
    sprite_path, duration_sec, metadata, created_at, updated_at
)
SELECT
    id, library_id, content_hash, path, filename, size_bytes, mtime,
    state, detected_language, title, description, poster_path,
    sprite_path, duration_sec, metadata, created_at, updated_at
  FROM videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE videos;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE _videos_03_rebuild RENAME TO videos;
-- +goose StatementEnd

-- +goose StatementBegin
-- Recreate the indexes from slot 0001 (the rebuild dropped them along
-- with the original table).
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

-- +goose StatementBegin
-- New helper from slot 0003.
CREATE INDEX IF NOT EXISTS videos_content_hash_lookup_idx
    ON videos (content_hash);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The down-migration is dev-only (per migrations/README.md §6) and
-- intentionally does not attempt the reverse rebuild — restoring the
-- global UNIQUE on a populated table would fail if cross-library
-- duplicates exist. Drop only what slot 0003 added.
DROP INDEX IF EXISTS videos_content_hash_lookup_idx;
-- +goose StatementEnd
