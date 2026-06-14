-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0084 (Epic 27 / Story 27.5).
--
-- UUID→TEXT, TIMESTAMPTZ→TEXT, BOOLEAN→INTEGER. The singleton CHECK
-- (id = 1) and the device-id/uuid are app-generated on first enable.
--
CREATE TABLE IF NOT EXISTS hdhr_device (
    id            INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    device_id     TEXT    NOT NULL,
    device_uuid   TEXT    NOT NULL,
    friendly_name TEXT    NOT NULL DEFAULT 'Maktaba',
    tuner_count   INTEGER NOT NULL DEFAULT 4,
    enabled       INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS hdhr_tuner_leases (
    id          TEXT PRIMARY KEY,
    channel_id  TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    client_addr TEXT NOT NULL,
    started_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS hdhr_tuner_leases_active_idx ON hdhr_tuner_leases (last_seen);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS hdhr_tuner_leases_active_idx;
DROP TABLE IF EXISTS hdhr_tuner_leases;
DROP TABLE IF EXISTS hdhr_device;
-- +goose StatementEnd
