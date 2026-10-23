-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0149: the update driver's run detail and the targets'
-- name, install kind, last report and last move (TEXT JSON and timestamps).
--
ALTER TABLE update_runs ADD COLUMN IF NOT EXISTS install_kind TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE update_runs ADD COLUMN IF NOT EXISTS detail TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE update_targets ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE update_targets ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE update_targets ADD COLUMN IF NOT EXISTS detail TEXT NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE update_targets ADD COLUMN IF NOT EXISTS updated_at TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
