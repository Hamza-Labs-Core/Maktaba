-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0004.
--
-- SQLite cannot ALTER TABLE … ADD CONSTRAINT (CHECK), so we rebuild
-- `videos` in-place with the new CHECK using the standard
-- create-new / copy / drop-old / rename pattern from
-- https://sqlite.org/lang_altertable.html#otheralter.
--
-- We do NOT touch `processing_jobs`: slot 0002's SQLite migration
-- already declares the canonical stage CHECK inline at table-creation
-- time, so there is nothing to add. The legacy 'thumb' → 'thumbnail'
-- rewrite is included for completeness even though the slot 0002 CHECK
-- would already reject any pre-0004 'thumb' rows.
--
-- SQLite has no LISTEN/NOTIFY, so the videos.state_changed trigger
-- from the Postgres file is intentionally absent here — Python callers
-- publish on the in-process PubsubBus after a successful state
-- transition commits.
--
-- The rebuild copies every column from the slot 0001/0007 shape:
--   id, library_id, content_hash, path, filename, size_bytes, mtime,
--   state, detected_language, title, description, poster_path,
--   sprite_path, duration_sec, metadata, created_at, updated_at,
--   last_seen_at, deleted_at.
--
UPDATE processing_jobs SET stage = 'thumbnail' WHERE stage = 'thumb';
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = OFF;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS videos__new (
    id                 TEXT     PRIMARY KEY,
    library_id         TEXT     NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    content_hash       TEXT     NOT NULL UNIQUE,
    path               TEXT     NOT NULL,
    filename           TEXT     NOT NULL,
    size_bytes         INTEGER  NOT NULL CHECK (size_bytes >= 0),
    mtime              TEXT     NOT NULL,
    state              TEXT     NOT NULL DEFAULT 'discovered'
                                CHECK (state IN (
                                    'discovered',
                                    'probed',
                                    'audio_extracted',
                                    'transcribed',
                                    'indexed',
                                    'thumbnailed',
                                    'ready',
                                    'ready_no_audio',
                                    'missing',
                                    'superseded',
                                    'corrupted',
                                    'failed'
                                )),
    detected_language  TEXT,
    title              TEXT,
    description        TEXT,
    poster_path        TEXT,
    sprite_path        TEXT,
    duration_sec       REAL,
    metadata           TEXT     NOT NULL DEFAULT '{}',
    created_at         TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at         TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at       TEXT,
    deleted_at         TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO videos__new (
    id, library_id, content_hash, path, filename, size_bytes, mtime,
    state, detected_language, title, description, poster_path,
    sprite_path, duration_sec, metadata, created_at, updated_at,
    last_seen_at, deleted_at
)
SELECT
    id, library_id, content_hash, path, filename, size_bytes, mtime,
    state, detected_language, title, description, poster_path,
    sprite_path, duration_sec, metadata, created_at, updated_at,
    last_seen_at, deleted_at
FROM videos;
-- +goose StatementEnd

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
DROP INDEX IF EXISTS videos_missing_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS videos;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos__new RENAME TO videos;
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

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_missing_idx
    ON videos (library_id, last_seen_at)
    WHERE state = 'missing';
-- +goose StatementEnd

-- +goose StatementBegin
PRAGMA foreign_keys = ON;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- The forward direction tightened the CHECK; the down direction would
-- need to rebuild videos a second time to drop it. That is dev-only
-- territory; we leave the CHECK in place on rollback to avoid the
-- bookkeeping burden. README §3 covers the down-migration policy.
SELECT 1;
-- +goose StatementEnd
