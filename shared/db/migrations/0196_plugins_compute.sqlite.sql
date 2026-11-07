-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0196 (plugins: local compute). Plugins run on a Postgres
-- home only (slot 0194); this keeps the schema in step.
--
CREATE TABLE IF NOT EXISTS plugin_compute_usage (
    plugin_id      TEXT    NOT NULL,
    day            TEXT    NOT NULL,
    generate_calls INTEGER NOT NULL DEFAULT 0,
    embed_texts    INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (plugin_id, day)
);

CREATE TABLE IF NOT EXISTS compute_leases (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    pool       TEXT NOT NULL,
    plugin_id  TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS compute_leases_pool_idx ON compute_leases (pool, expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS compute_leases;
DROP TABLE IF EXISTS plugin_compute_usage;
-- +goose StatementEnd
