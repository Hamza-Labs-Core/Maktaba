-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS subtitle_files (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id      TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    transcript_id TEXT    REFERENCES transcripts(id) ON DELETE SET NULL,
    language      TEXT    NOT NULL,
    format        TEXT    NOT NULL CHECK (format IN ('srt','vtt')),
    source        TEXT    NOT NULL CHECK (source IN ('embedded','generated','external')),
    path          TEXT    NOT NULL,
    byte_size     INTEGER,
    sha256        TEXT,
    is_embedded   INTEGER NOT NULL DEFAULT 0,
    is_external   INTEGER NOT NULL DEFAULT 0,
    metadata      TEXT    NOT NULL DEFAULT '{}',
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    deleted_at    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS subtitle_files_active_unique
    ON subtitle_files (video_id, language, format, source)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS subtitle_files_video_idx
    ON subtitle_files (video_id) WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS subtitle_files;
-- +goose StatementEnd
