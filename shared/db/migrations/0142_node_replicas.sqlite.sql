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

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
