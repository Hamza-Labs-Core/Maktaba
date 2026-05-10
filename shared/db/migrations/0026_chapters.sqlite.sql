-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS chapters (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    video_id        TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    transcript_id   INTEGER REFERENCES transcripts(id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL CHECK (seq >= 0),
    start_sec       REAL    NOT NULL CHECK (start_sec >= 0),
    end_sec         REAL    NOT NULL,
    title           TEXT,
    source          TEXT    NOT NULL DEFAULT 'inferred'
                            CHECK (source IN ('inferred','embedded','manual')),
    lang            TEXT,
    confidence      REAL CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    metadata        TEXT    NOT NULL DEFAULT '{}',
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    UNIQUE (video_id, source, seq),
    CHECK (end_sec >= start_sec)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS chapters_video_start_idx
    ON chapters (video_id, start_sec);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS chapters_video_start_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS chapters;
-- +goose StatementEnd
