-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS chapters (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id   TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    start_sec  REAL    NOT NULL CHECK (start_sec >= 0),
    end_sec    REAL    CHECK (end_sec IS NULL OR end_sec > start_sec),
    title      TEXT    NOT NULL,
    source     TEXT    NOT NULL DEFAULT 'manual'
               CHECK (source IN ('embedded','inferred','manual')),
    metadata   TEXT    NOT NULL DEFAULT '{}',
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE (video_id, seq)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS chapters_video_idx ON chapters (video_id, seq);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS chapters_video_idx;
DROP TABLE IF EXISTS chapters;
-- +goose StatementEnd
