-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS subtitle_files (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id        TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    transcript_id   INTEGER REFERENCES transcripts(id) ON DELETE SET NULL,
    format          TEXT    NOT NULL CHECK (format IN ('srt','vtt','ass','ssa')),
    language        TEXT    NOT NULL,
    path            TEXT    NOT NULL,
    is_external     INTEGER NOT NULL DEFAULT 0,
    is_embedded     INTEGER NOT NULL DEFAULT 0,
    is_default      INTEGER NOT NULL DEFAULT 0,
    flags           TEXT    NOT NULL DEFAULT '{}',
    track_index     INTEGER,
    size_bytes      INTEGER,
    mtime_ns        INTEGER,
    metadata        TEXT    NOT NULL DEFAULT '{}',
    revived_count   INTEGER NOT NULL DEFAULT 0,
    deleted_at      TEXT,
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    CHECK (NOT (is_external = 1 AND is_embedded = 1)),
    CHECK ((is_embedded = 0) OR (is_embedded = 1 AND track_index IS NOT NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS subtitle_files_video_lang_idx
    ON subtitle_files (video_id, language);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS subtitle_files_video_default_idx
    ON subtitle_files (video_id, is_default DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS subtitle_files_internal_uq
    ON subtitle_files (video_id, format, language)
    WHERE is_external = 0 AND is_embedded = 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS subtitle_files_embedded_uq
    ON subtitle_files (video_id, track_index)
    WHERE is_embedded = 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS subtitle_files_embedded_uq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS subtitle_files_internal_uq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS subtitle_files_video_default_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS subtitle_files_video_lang_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS subtitle_files;
-- +goose StatementEnd
