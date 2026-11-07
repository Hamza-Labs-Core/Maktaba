-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0196 (plugins: what a package's signatures prove).
-- Plugins run on a Postgres home only (slot 0194); the columns keep the schema in step.
-- SQLite cannot widen slot 0194's inline review_level check without rebuilding the table,
-- and no SQLite home installs a plugin, so the check stays as it was.
--
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS developer_key_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS reviewed_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS previous_review_level TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS previous_developer_key_id TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE plugin_installs ADD COLUMN IF NOT EXISTS previous_reviewed_at TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
