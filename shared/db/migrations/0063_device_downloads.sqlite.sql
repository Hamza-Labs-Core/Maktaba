-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0063 (Epic 12 Story 12.11).
-- Same shape as the Postgres table; UUIDs are stored as TEXT and the
-- CONCURRENTLY index keyword (Postgres-only) is dropped.
CREATE TABLE IF NOT EXISTS device_downloads (
    device_id    TEXT     NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    video_id     TEXT     NOT NULL,
    quality      TEXT,
    size_bytes   INTEGER,
    checksum     TEXT,
    created_at   TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at   TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked      INTEGER  NOT NULL DEFAULT 0,
    PRIMARY KEY (device_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS device_downloads_video_idx
    ON device_downloads (video_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS device_downloads_video_idx;
DROP TABLE IF EXISTS device_downloads;
-- +goose StatementEnd
