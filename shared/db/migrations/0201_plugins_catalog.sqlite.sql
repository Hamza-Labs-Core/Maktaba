-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0201 (plugins: the catalog and the kill switch). Plugins run
-- on a Postgres home only (slot 0194); this keeps the schema in step.
--
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS revoked INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS revocation TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS auto_update INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS plugin_trust (
    id         INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    state      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE TABLE IF NOT EXISTS plugin_lists (
    name       TEXT PRIMARY KEY,
    body       TEXT NOT NULL,
    version    INTEGER NOT NULL,
    sha256     TEXT NOT NULL,
    fetched_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plugin_lists;
DROP TABLE IF EXISTS plugin_trust;
-- +goose StatementEnd
