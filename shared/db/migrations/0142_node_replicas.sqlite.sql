-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0142 (node replicas). A SQLite home has no machines
-- and no roles; the columns exist so the same code runs on both (the reporting
-- function is Postgres only: no machine reaches a SQLite home's database).
--
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS replica TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS replica_reported_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_replica_beat (
    id  INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO cluster_replica_beat (id) VALUES (1);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_replica_beat;
-- +goose StatementEnd
