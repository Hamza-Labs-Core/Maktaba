-- +goose Up
-- +goose StatementBegin
-- SQLite already has `transcripts_fts` as a virtual table (slot 0020).
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
