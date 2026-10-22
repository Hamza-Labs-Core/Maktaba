-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0148 (a user's searches, their saved result sets and
-- the Recent searches list). Same columns; times are Unix milliseconds on both.
--
CREATE TABLE IF NOT EXISTS search_runs (
    id               TEXT    PRIMARY KEY,
    user_id          TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    query            TEXT    NOT NULL,
    mode             TEXT    NOT NULL,
    filters          TEXT    NOT NULL DEFAULT '{}',
    title_count      INTEGER NOT NULL DEFAULT 0,
    transcript_count INTEGER NOT NULL DEFAULT 0,
    degraded         INTEGER NOT NULL DEFAULT 0,
    result           TEXT,
    created_at_ms    INTEGER NOT NULL,
    last_used_at_ms  INTEGER NOT NULL,
    expires_at_ms    INTEGER NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS search_runs_user_recent_idx
    ON search_runs (user_id, created_at_ms DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS search_runs_kept_idx
    ON search_runs (expires_at_ms) WHERE result IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS search_runs;
-- +goose StatementEnd
