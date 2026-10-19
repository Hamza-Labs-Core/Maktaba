-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0117 (Epic 27, TV core). TIMESTAMPTZ becomes TEXT.
-- SQLite cannot change a column default in place, so hdhr_device (a singleton) is rebuilt
-- with tuner_count DEFAULT 20; its row is copied as it is.
--
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS host_id TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS stream_base_url TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS encoder TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS restarts INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS nvenc_fallbacks INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_runtime ADD COLUMN IF NOT EXISTS heartbeat_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS streaming_hosts (
    host_id          TEXT    PRIMARY KEY,
    host             TEXT    NOT NULL,
    role             TEXT    NOT NULL DEFAULT 'home' CHECK (role IN ('home','node')),
    stream_base_url  TEXT,
    encoder          TEXT,
    max_active       INTEGER NOT NULL DEFAULT 0,
    active_channels  INTEGER NOT NULL DEFAULT 0,
    nvenc_free       INTEGER NOT NULL DEFAULT 0,
    vt_free          INTEGER NOT NULL DEFAULT 0,
    started_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    heartbeat_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

DROP TABLE IF EXISTS hdhr_device_new_0117;
CREATE TABLE IF NOT EXISTS hdhr_device_new_0117 (
    id            INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    device_id     TEXT    NOT NULL,
    device_uuid   TEXT    NOT NULL,
    friendly_name TEXT    NOT NULL DEFAULT 'Maktaba',
    tuner_count   INTEGER NOT NULL DEFAULT 20,
    enabled       INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO hdhr_device_new_0117 (id, device_id, device_uuid, friendly_name, tuner_count, enabled, created_at)
    SELECT id, device_id, device_uuid, friendly_name, tuner_count, enabled, created_at FROM hdhr_device;
DROP TABLE IF EXISTS hdhr_device;
ALTER TABLE hdhr_device_new_0117 RENAME TO hdhr_device;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS hdhr_device_old_0117;
CREATE TABLE IF NOT EXISTS hdhr_device_old_0117 (
    id            INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    device_id     TEXT    NOT NULL,
    device_uuid   TEXT    NOT NULL,
    friendly_name TEXT    NOT NULL DEFAULT 'Maktaba',
    tuner_count   INTEGER NOT NULL DEFAULT 4,
    enabled       INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
INSERT INTO hdhr_device_old_0117 (id, device_id, device_uuid, friendly_name, tuner_count, enabled, created_at)
    SELECT id, device_id, device_uuid, friendly_name, tuner_count, enabled, created_at FROM hdhr_device;
DROP TABLE IF EXISTS hdhr_device;
ALTER TABLE hdhr_device_old_0117 RENAME TO hdhr_device;
DROP TABLE IF EXISTS streaming_hosts;
ALTER TABLE channel_runtime DROP COLUMN heartbeat_at;
ALTER TABLE channel_runtime DROP COLUMN nvenc_fallbacks;
ALTER TABLE channel_runtime DROP COLUMN restarts;
ALTER TABLE channel_runtime DROP COLUMN encoder;
ALTER TABLE channel_runtime DROP COLUMN stream_base_url;
ALTER TABLE channel_runtime DROP COLUMN host_id;
-- +goose StatementEnd
