-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0195 (plugins: keep-off and health). Plugins run on a
-- Postgres home only (slot 0194); this keeps the schema in step.
--
CREATE TABLE IF NOT EXISTS plugin_keep_off (
    plugin_id TEXT PRIMARY KEY,
    set_by    TEXT NOT NULL DEFAULT '',
    set_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS plugin_health (
    plugin_id       TEXT PRIMARY KEY,
    tripped         INTEGER NOT NULL DEFAULT 0,
    tripped_at      TEXT,
    trip_reason     TEXT NOT NULL DEFAULT '',
    recent          TEXT NOT NULL DEFAULT '[]',
    crashes         TEXT NOT NULL DEFAULT '[]',
    refusals        TEXT NOT NULL DEFAULT '[]',
    jobs_done       INTEGER NOT NULL DEFAULT 0,
    jobs_failed     INTEGER NOT NULL DEFAULT 0,
    last_error_code TEXT NOT NULL DEFAULT '',
    last_error_at   TEXT,
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plugin_health;
DROP TABLE IF EXISTS plugin_keep_off;
-- +goose StatementEnd
