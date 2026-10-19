-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0126 (Epic 32, story 32.13): simulcast through
-- Cloudflare live outputs. A SQLite home is a single machine: no roles. UUID becomes
-- TEXT, JSONB TEXT, NUMERIC REAL, TIMESTAMPTZ TEXT, BOOLEAN INTEGER.
--
CREATE TABLE IF NOT EXISTS tv_simulcast_destinations (
    id          TEXT    PRIMARY KEY,
    name        TEXT    NOT NULL,
    kind        TEXT    NOT NULL CHECK (kind IN ('youtube', 'facebook', 'rtmp')),
    ingest_url  TEXT    NOT NULL,
    key_secret  TEXT    NOT NULL,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS tv_simulcast_attachments (
    id                   TEXT    PRIMARY KEY,
    channel_id           TEXT    NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    destination_id       TEXT    NOT NULL REFERENCES tv_simulcast_destinations(id) ON DELETE CASCADE,
    mode                 TEXT    NOT NULL CHECK (mode IN ('always', 'schedule')),
    rules                TEXT    NOT NULL DEFAULT '[]',
    enabled              INTEGER NOT NULL DEFAULT 0,
    manual               TEXT    CHECK (manual IN ('start', 'stop')),
    manual_until         TEXT,
    rights_confirmed_by  TEXT,
    rights_confirmed_at  TEXT,
    created_at           TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at           TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (channel_id, destination_id)
);

CREATE TABLE IF NOT EXISTS tv_simulcast_outputs (
    id              TEXT    PRIMARY KEY,
    attachment_id   TEXT    REFERENCES tv_simulcast_attachments(id) ON DELETE SET NULL,
    channel_id      TEXT    REFERENCES channels(id) ON DELETE SET NULL,
    destination_id  TEXT    REFERENCES tv_simulcast_destinations(id) ON DELETE SET NULL,
    push_id         TEXT    REFERENCES tv_remote_pushes(id) ON DELETE SET NULL,
    input_id        TEXT    REFERENCES tv_remote_inputs(id) ON DELETE SET NULL,
    input_uid       TEXT,
    cf_output_uid   TEXT,
    state           TEXT    NOT NULL
                            CHECK (state IN ('scheduled', 'starting', 'live', 'stopping', 'off', 'errored')),
    window_start    TEXT,
    window_end      TEXT,
    started_at      TEXT,
    ended_at        TEXT,
    minutes         REAL    NOT NULL DEFAULT 0,
    detail          TEXT,
    error           TEXT,
    cf_status       TEXT,
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS tv_simulcast_outputs_open_uniq
    ON tv_simulcast_outputs (attachment_id) WHERE ended_at IS NULL;
CREATE INDEX IF NOT EXISTS tv_simulcast_outputs_input_idx
    ON tv_simulcast_outputs (input_id) WHERE ended_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE tv_remote_pushes ADD COLUMN IF NOT EXISTS holds TEXT NOT NULL DEFAULT '[]';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE tv_remote_usage_daily ADD COLUMN IF NOT EXISTS simulcast_minutes REAL NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tv_simulcast_outputs;
DROP TABLE IF EXISTS tv_simulcast_attachments;
DROP TABLE IF EXISTS tv_simulcast_destinations;
ALTER TABLE tv_remote_usage_daily DROP COLUMN simulcast_minutes;
ALTER TABLE tv_remote_pushes DROP COLUMN holds;
-- +goose StatementEnd
