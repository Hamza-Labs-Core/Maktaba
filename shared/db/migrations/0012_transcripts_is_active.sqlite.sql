-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0012.
--
CREATE TABLE IF NOT EXISTS transcripts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id        TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    language_code   TEXT    NOT NULL,
    backend         TEXT    NOT NULL,
    model           TEXT,
    state           TEXT    NOT NULL DEFAULT 'running'
                            CHECK (state IN ('running','done','paused','failed','superseded')),
    is_active       INTEGER NOT NULL DEFAULT 1,
    last_indexed_segment_seq INTEGER NOT NULL DEFAULT 0,
    metadata        TEXT    NOT NULL DEFAULT '{}',
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at     TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS transcripts_video_idx
    ON transcripts (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS transcripts_video_active_uq
    ON transcripts (video_id) WHERE is_active = 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS transcripts_video_active_uq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS transcripts_video_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS transcripts;
-- +goose StatementEnd
