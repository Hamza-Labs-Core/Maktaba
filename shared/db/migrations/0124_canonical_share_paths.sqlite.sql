-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0124 (Epic 31 / story 31.3): UUID and TIMESTAMPTZ
-- become TEXT, JSONB becomes JSON text; no roles.
--
CREATE TABLE IF NOT EXISTS storage_servers (
    id          TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
    name        TEXT NOT NULL UNIQUE
                CHECK (name = lower(name) AND name <> '' AND instr(name, '/') = 0),
    addresses   TEXT NOT NULL DEFAULT '[]',
    char_map    TEXT NOT NULL DEFAULT 'none',
    exports     TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS storage_path_migrations (
    batch_id     TEXT NOT NULL,
    table_name   TEXT NOT NULL,
    column_name  TEXT NOT NULL,
    row_key      TEXT NOT NULL,
    old_value    TEXT NOT NULL,
    new_value    TEXT NOT NULL,
    machine      TEXT NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    reverted_at  TEXT,
    PRIMARY KEY (batch_id, table_name, column_name, row_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE instance_identity ADD COLUMN IF NOT EXISTS storage TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE instance_identity ADD COLUMN IF NOT EXISTS path_map TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS storage_path_migrations;
DROP TABLE IF EXISTS storage_servers;
-- +goose StatementEnd
