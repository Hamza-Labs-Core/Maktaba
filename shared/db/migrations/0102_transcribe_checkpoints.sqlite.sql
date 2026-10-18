-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0102 (plan 03-10): a transcription's progress, saved
-- about once a minute so that a restarted worker resumes the file. Same columns as
-- Postgres (segments as JSON text).
--
CREATE TABLE IF NOT EXISTS transcribe_checkpoints (
    video_id     TEXT PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    fingerprint  TEXT NOT NULL,
    seek_frames  INTEGER NOT NULL CHECK (seek_frames >= 0),
    last_speech  REAL NOT NULL DEFAULT 0,
    language     TEXT,
    segments     TEXT NOT NULL DEFAULT '[]',
    updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcribe_checkpoints;
-- +goose StatementEnd
