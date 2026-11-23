-- +goose Up
-- +goose StatementBegin
-- Slot 0236 (Epic 49, story 49.10 follow-up): a search's order and its filters as asked,
-- as on Postgres (NULL on an older row). ADD COLUMN IF NOT EXISTS is the API's SQLite
-- migrator's (api/migrate_sqlite.go).
ALTER TABLE search_runs ADD COLUMN IF NOT EXISTS sort_order TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE search_runs ADD COLUMN IF NOT EXISTS asked_filters TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE search_runs DROP COLUMN asked_filters;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE search_runs DROP COLUMN sort_order;
-- +goose StatementEnd
