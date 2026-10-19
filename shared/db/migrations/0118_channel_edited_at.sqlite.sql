-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0118 (Epic 27, channel builder): TIMESTAMPTZ becomes TEXT.
-- No backfill: a SQLite database has no lineup plan writer history worth guessing from.
--
ALTER TABLE channels ADD COLUMN edited_at TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE channels DROP COLUMN edited_at;
-- +goose StatementEnd
