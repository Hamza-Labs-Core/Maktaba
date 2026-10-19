-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0125 (Epic 32, 32.7/32.8/32.10): remote viewing through
-- Cloudflare Stream. A SQLite home is a single machine: no roles, no functions (the push
-- relay runs only against Postgres). UUID becomes TEXT, JSONB TEXT, NUMERIC REAL,
-- TIMESTAMPTZ TEXT.
--
CREATE TABLE IF NOT EXISTS tv_remote_inputs (
    id              TEXT    PRIMARY KEY,
    cf_uid          TEXT    NOT NULL UNIQUE,
    name            TEXT    NOT NULL,
    state           TEXT    NOT NULL DEFAULT 'idle'
                            CHECK (state IN ('idle', 'assigned', 'broadcasting', 'cleanup', 'errored')),
    channel_id      TEXT    REFERENCES channels(id) ON DELETE SET NULL,
    ingest          TEXT    NOT NULL DEFAULT '{}',
    last_video_uid  TEXT,
    assigned_at     TEXT,
    error           TEXT,
    error_at        TEXT,
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS tv_remote_pushes (
    id                    TEXT    PRIMARY KEY,
    channel_id            TEXT    NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    input_id              TEXT    REFERENCES tv_remote_inputs(id) ON DELETE SET NULL,
    host_id               TEXT,
    generation            INTEGER,
    state                 TEXT    NOT NULL
                                  CHECK (state IN ('requested', 'connecting', 'live', 'stopping',
                                                   'cleanup', 'done', 'failed')),
    attempt               INTEGER NOT NULL DEFAULT 1,
    ingest                TEXT    NOT NULL DEFAULT 'rtmps' CHECK (ingest IN ('rtmps', 'srt')),
    video_uid             TEXT,
    started_at            TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    state_at              TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    live_at               TEXT,
    idle_since            TEXT,
    ended_at              TEXT,
    end_reason            TEXT,
    detail                TEXT,
    relay_state           TEXT,
    relay_detail          TEXT,
    relay_at              TEXT,
    minutes_delivered     REAL    NOT NULL DEFAULT 0,
    recording_deleted_at  TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS tv_remote_pushes_one_per_channel
    ON tv_remote_pushes (channel_id) WHERE state IN ('requested', 'connecting', 'live', 'stopping');
CREATE INDEX IF NOT EXISTS tv_remote_pushes_state_idx
    ON tv_remote_pushes (state) WHERE state NOT IN ('done', 'failed');

CREATE TABLE IF NOT EXISTS tv_remote_sessions (
    id            TEXT    PRIMARY KEY,
    user_id       TEXT    NOT NULL,
    channel_id    TEXT    NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    push_id       TEXT    REFERENCES tv_remote_pushes(id) ON DELETE SET NULL,
    device        TEXT    NOT NULL DEFAULT '',
    started_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_beat_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ended_at      TEXT,
    end_reason    TEXT,
    token_exp     TEXT,
    token_kid     TEXT,
    video_uid     TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS tv_remote_sessions_open_uniq
    ON tv_remote_sessions (user_id, channel_id, device) WHERE ended_at IS NULL;
CREATE INDEX IF NOT EXISTS tv_remote_sessions_push_idx
    ON tv_remote_sessions (push_id) WHERE ended_at IS NULL;

CREATE TABLE IF NOT EXISTS tv_remote_usage_daily (
    day                     TEXT    PRIMARY KEY,
    minutes_delivered       REAL    NOT NULL DEFAULT 0,
    peak_recording_minutes  REAL    NOT NULL DEFAULT 0,
    cost_usd                REAL    NOT NULL DEFAULT 0,
    source                  TEXT    NOT NULL DEFAULT 'sessions' CHECK (source IN ('analytics', 'sessions')),
    updated_at              TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS tv_push_secrets (
    push_id     TEXT    PRIMARY KEY REFERENCES tv_remote_pushes(id) ON DELETE CASCADE,
    url         TEXT    NOT NULL,
    key         TEXT    NOT NULL,
    stream_id   TEXT,
    expires_at  TEXT    NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tv_push_secrets;
DROP TABLE IF EXISTS tv_remote_usage_daily;
DROP TABLE IF EXISTS tv_remote_sessions;
DROP TABLE IF EXISTS tv_remote_pushes;
DROP TABLE IF EXISTS tv_remote_inputs;
-- +goose StatementEnd
