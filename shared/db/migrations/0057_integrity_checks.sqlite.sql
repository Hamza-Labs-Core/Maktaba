-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS integrity_checks (
    id              TEXT    PRIMARY KEY,
    video_id        TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    checked_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    file_present    INTEGER NOT NULL,
    size_bytes      INTEGER,
    content_hash    TEXT,
    segments_count  INTEGER,
    transcripts_ok  INTEGER,
    error           TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS integrity_checks_video
    ON integrity_checks (video_id, checked_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS integrity_checks_video;
DROP TABLE IF EXISTS integrity_checks;
-- +goose StatementEnd
