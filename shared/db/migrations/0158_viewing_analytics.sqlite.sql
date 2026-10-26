-- +goose Up
--
-- SQLite parity sibling for slot 0158 (Epic 44, story 44.1): analytics counts every way
-- of watching (plays recorded from the stream session, Live TV viewings) and every app's
-- use of the API. See the Postgres file for the tables' meaning.
ALTER TABLE watch_sessions ADD COLUMN IF NOT EXISTS stream_session_id TEXT;
ALTER TABLE watch_sessions ADD COLUMN IF NOT EXISTS client_app TEXT;
ALTER TABLE watch_sessions ADD COLUMN IF NOT EXISTS client_version TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS watch_sessions_stream_session_idx
    ON watch_sessions (stream_session_id) WHERE stream_session_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS tv_watch_sessions (
    id               TEXT    PRIMARY KEY,
    channel_id       TEXT    NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id          TEXT    REFERENCES users(id) ON DELETE CASCADE,
    surface          TEXT    NOT NULL CHECK (surface IN ('app', 'tuner', 'remote')),
    client_app       TEXT,
    client_version   TEXT,
    platform         TEXT,
    device_type      TEXT,
    client_addr_hash TEXT,
    started_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ended_at         TEXT,
    seconds          INTEGER NOT NULL DEFAULT 0 CHECK (seconds >= 0),
    end_reason       TEXT    CHECK (end_reason IN ('closed', 'idle', 'replaced'))
);
CREATE INDEX IF NOT EXISTS tv_watch_sessions_started_idx ON tv_watch_sessions (started_at);
CREATE INDEX IF NOT EXISTS tv_watch_sessions_channel_idx ON tv_watch_sessions (channel_id, started_at);
CREATE INDEX IF NOT EXISTS tv_watch_sessions_open_idx
    ON tv_watch_sessions (last_seen_at) WHERE ended_at IS NULL;

CREATE TABLE IF NOT EXISTS api_usage_daily (
    day        TEXT    NOT NULL,
    client_app TEXT    NOT NULL,
    platform   TEXT    NOT NULL,
    requests   INTEGER NOT NULL DEFAULT 0 CHECK (requests >= 0),
    errors     INTEGER NOT NULL DEFAULT 0 CHECK (errors >= 0),
    total_ms   INTEGER NOT NULL DEFAULT 0 CHECK (total_ms >= 0),
    slow       INTEGER NOT NULL DEFAULT 0 CHECK (slow >= 0),
    PRIMARY KEY (day, client_app, platform)
);
CREATE TABLE IF NOT EXISTS api_usage_people_daily (
    day            TEXT NOT NULL,
    client_app     TEXT NOT NULL,
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_version TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (day, client_app, user_id)
);

-- +goose Down
DROP TABLE IF EXISTS api_usage_people_daily;
DROP TABLE IF EXISTS api_usage_daily;
DROP INDEX IF EXISTS tv_watch_sessions_open_idx;
DROP INDEX IF EXISTS tv_watch_sessions_channel_idx;
DROP INDEX IF EXISTS tv_watch_sessions_started_idx;
DROP TABLE IF EXISTS tv_watch_sessions;
DROP INDEX IF EXISTS watch_sessions_stream_session_idx;
ALTER TABLE watch_sessions DROP COLUMN client_version;
ALTER TABLE watch_sessions DROP COLUMN client_app;
ALTER TABLE watch_sessions DROP COLUMN stream_session_id;
