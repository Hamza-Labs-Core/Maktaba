-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS transcript_units (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    transcript_id   TEXT    NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    video_id        TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    segment_id      INTEGER REFERENCES transcript_segments(id) ON DELETE CASCADE,
    unit_index      INTEGER NOT NULL,
    start_sec       REAL    NOT NULL,
    end_sec         REAL    NOT NULL,
    text            TEXT    NOT NULL,
    language        TEXT,
    embedding_id    TEXT,
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (transcript_id, unit_index)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_units_video_idx
    ON transcript_units (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_units_segment_idx
    ON transcript_units (segment_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcript_units_time_idx
    ON transcript_units (transcript_id, start_sec);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS transcript_units;
-- +goose StatementEnd
