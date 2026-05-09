-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0007.
--
-- SQLite 3.35+ supports `ADD COLUMN IF NOT EXISTS`. Substitutions:
-- TIMESTAMPTZ → TEXT (ISO-8601). SQLite supports partial indexes
-- since 3.8.
--
-- Note: SQLite 3.35 also added `DROP COLUMN`, but earlier versions
-- did not. The down-migration uses IF EXISTS for safety.
--
ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS last_seen_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos
    ADD COLUMN IF NOT EXISTS deleted_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_missing_idx
    ON videos (library_id, last_seen_at)
    WHERE state = 'missing';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_missing_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS deleted_at;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos DROP COLUMN IF EXISTS last_seen_at;
-- +goose StatementEnd
