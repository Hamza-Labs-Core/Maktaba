-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0204 (plugins: entitlements). Plugins run on a Postgres home
-- only (slot 0194); this keeps the schema in step.
--
CREATE TABLE IF NOT EXISTS plugin_entitlements (
    purchase_id  TEXT PRIMARY KEY,
    plugin_id    TEXT NOT NULL,
    product      TEXT NOT NULL,
    store_home   TEXT NOT NULL,
    valid_from   TEXT NOT NULL,
    valid_until  TEXT NOT NULL,
    issued_at    TEXT NOT NULL,
    envelope     TEXT NOT NULL,
    refreshed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX IF NOT EXISTS plugin_entitlements_plugin_idx ON plugin_entitlements (plugin_id, valid_until);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plugin_entitlements;
-- +goose StatementEnd
