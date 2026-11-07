-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0202 (plugins: the version a plugin's stored data is at).
-- Plugins run on a Postgres home only (slot 0194); the column keeps the schema in step.
--
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS data_version INTEGER;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
