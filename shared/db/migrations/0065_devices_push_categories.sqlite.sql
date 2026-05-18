-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0065 (Epic 12 Story 12.10).
-- SQLite 3.35+ supports `ADD COLUMN IF NOT EXISTS`. JSONB → TEXT
-- (SQLite stores JSON as TEXT; json1 functions operate on it).
ALTER TABLE devices ADD COLUMN IF NOT EXISTS os_version TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE devices ADD COLUMN IF NOT EXISTS categories TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE devices DROP COLUMN IF EXISTS categories;
ALTER TABLE devices DROP COLUMN IF EXISTS os_version;
-- +goose StatementEnd
