-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transcripts (
    id                  TEXT    PRIMARY KEY,
    video_id            TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    audio_track_id      INTEGER NOT NULL REFERENCES audio_tracks(id),
    language            TEXT    NOT NULL,
    detected_language   TEXT,
    language_confidence REAL,
    backend             TEXT    NOT NULL,
    model               TEXT    NOT NULL,
    backend_version     TEXT,
    word_level          INTEGER NOT NULL DEFAULT 0,
    diarized            INTEGER NOT NULL DEFAULT 0,
    quality_score       REAL,
    is_active           INTEGER NOT NULL DEFAULT 1,
    metadata            TEXT    NOT NULL DEFAULT '{}',
    superseded_at       TEXT,
    created_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS transcripts_active_unique
    ON transcripts (video_id, audio_track_id)
    WHERE is_active = 1;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcripts_video_active_idx
    ON transcripts (video_id) WHERE is_active = 1;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transcript_segments (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    transcript_id TEXT    NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    start_sec     REAL    NOT NULL,
    end_sec       REAL    NOT NULL,
    text          TEXT    NOT NULL,
    speaker       TEXT,
    confidence    REAL,
    metadata      TEXT    NOT NULL DEFAULT '{}',
    UNIQUE (transcript_id, seq)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_segments_time_idx
    ON transcript_segments (transcript_id, start_sec);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transcript_words (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    segment_id  INTEGER NOT NULL REFERENCES transcript_segments(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    start_sec   REAL    NOT NULL,
    end_sec     REAL    NOT NULL,
    text        TEXT    NOT NULL,
    confidence  REAL,
    UNIQUE (segment_id, seq)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_words;
DROP TABLE IF EXISTS transcript_segments;
DROP TABLE IF EXISTS transcripts;
-- +goose StatementEnd
