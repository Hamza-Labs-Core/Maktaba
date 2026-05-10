-- +goose Up
-- +goose StatementBegin
-- SQLite stores JSON as TEXT; no column-type change is needed for
-- `processing_jobs.error`. Callers should marshal to JSON text on the
-- way in and parse on the way out.
CREATE TABLE IF NOT EXISTS audio_cache (
    content_hash   TEXT PRIMARY KEY,
    video_id       TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    audio_track_id INTEGER NOT NULL REFERENCES audio_tracks(id) ON DELETE CASCADE,
    path           TEXT NOT NULL,
    bytes          INTEGER,
    created_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS audio_cache_video_idx ON audio_cache (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- SQLite 3.35+ supports `ADD COLUMN IF NOT EXISTS`.
ALTER TABLE audio_tracks
    ADD COLUMN IF NOT EXISTS last_extracted_at TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS audio_cache;
-- +goose StatementEnd
