-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0145 (Epic 38, follow-up of story 38.8): the second
-- listen's tries per video (relisten jobs), their backoff and where they stopped. Same
-- columns as Postgres (UUIDs and timestamps as TEXT).
--
CREATE TABLE IF NOT EXISTS relisten_attempts (
    video_id     TEXT    PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    attempts     INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_job_id  INTEGER,
    last_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    next_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    stopped_at   TEXT
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS relisten_attempts;
-- +goose StatementEnd
