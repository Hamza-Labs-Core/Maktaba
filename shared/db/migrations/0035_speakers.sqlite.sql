-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS speakers (
    id            TEXT PRIMARY KEY,
    video_id      TEXT REFERENCES videos(id) ON DELETE CASCADE,
    cluster_label TEXT,
    name          TEXT NOT NULL,
    metadata      TEXT NOT NULL DEFAULT '{}',
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (video_id, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS segment_speakers (
    segment_id INTEGER NOT NULL REFERENCES transcript_segments(id) ON DELETE CASCADE,
    speaker_id TEXT    NOT NULL REFERENCES speakers(id) ON DELETE CASCADE,
    confidence REAL,
    PRIMARY KEY (segment_id, speaker_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS segment_speakers_speaker_idx
    ON segment_speakers (speaker_id, segment_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS segment_speakers_speaker_idx;
DROP TABLE IF EXISTS segment_speakers;
DROP TABLE IF EXISTS speakers;
-- +goose StatementEnd
