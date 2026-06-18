-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0086 (Epic 29 / Story 29.1).
--
-- Same shape as the Postgres table without CONCURRENTLY / NO TRANSACTION.
-- UUIDs are TEXT, timestamps are TEXT (datetime('now')), BOOLEAN is the
-- usual 0/1 INTEGER. The CHECK constraints are inline (SQLite has no
-- ALTER-constraint, but this is a fresh table so that is moot).
--
CREATE TABLE IF NOT EXISTS watch_sessions (
    id               TEXT    PRIMARY KEY,
    user_id          TEXT    NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    video_id         TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    started_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    ended_at         TEXT,
    last_heartbeat   TEXT    NOT NULL DEFAULT (datetime('now')),
    duration_sec     INTEGER NOT NULL DEFAULT 0 CHECK (duration_sec >= 0),
    percent_complete REAL    NOT NULL DEFAULT 0 CHECK (percent_complete >= 0 AND percent_complete <= 100),
    state            TEXT    NOT NULL DEFAULT 'active'
                             CHECK (state IN ('active','completed','stopped','interrupted')),
    device_type      TEXT,
    platform         TEXT,
    quality          TEXT,
    ip_addr_hash     TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS watch_sessions_user_started_idx
    ON watch_sessions (user_id, started_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS watch_sessions_active_idx
    ON watch_sessions (last_heartbeat)
    WHERE state = 'active';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS watch_sessions_video_idx
    ON watch_sessions (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS watch_sessions_started_idx
    ON watch_sessions (started_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS user_analytics_prefs (
    user_id       TEXT    PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    track_enabled INTEGER NOT NULL DEFAULT 1,
    updated_at    TEXT    NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS user_analytics_prefs;
DROP INDEX IF EXISTS watch_sessions_started_idx;
DROP INDEX IF EXISTS watch_sessions_video_idx;
DROP INDEX IF EXISTS watch_sessions_active_idx;
DROP INDEX IF EXISTS watch_sessions_user_started_idx;
DROP TABLE IF EXISTS watch_sessions;
-- +goose StatementEnd
