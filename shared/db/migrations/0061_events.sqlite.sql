-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0061 (Epic 19 Story 19.2, HLB-353).
--
-- The cross-replica WS event bus is a Postgres LISTEN/NOTIFY concern:
-- a single-process SQLite deployment has exactly one API replica, so
-- the in-process ws.Hub already fans out every event and there is no
-- cross-replica gap to close. SQLite also has no LISTEN/NOTIFY, so
-- the Postgres trigger has no SQL-level equivalent here.
--
-- We still create the `events` table on SQLite so the on-connect
-- replay handshake (`last_event_id`) and the 7-day pruner work
-- identically on both backends — the only thing absent on SQLite is
-- the NOTIFY trigger (the single replica needs no wakeup; it writes
-- the row and fans out in-process). BIGSERIAL → INTEGER PRIMARY KEY
-- AUTOINCREMENT keeps the monotonic-id contract.
--
-- This file uses goose's StatementBegin / StatementEnd markers.
--
CREATE TABLE IF NOT EXISTS events (
    id          INTEGER     PRIMARY KEY AUTOINCREMENT,
    channel     TEXT        NOT NULL,
    type        TEXT        NOT NULL,
    payload     TEXT        NOT NULL DEFAULT '{}',
    created_at  TIMESTAMP   NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS events_channel_id ON events (channel, id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS events_created_at ON events (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS events_created_at;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS events_channel_id;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS events;
-- +goose StatementEnd
