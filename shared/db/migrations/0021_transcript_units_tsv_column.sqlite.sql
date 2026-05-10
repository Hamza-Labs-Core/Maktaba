-- +goose Up
-- +goose StatementBegin
-- SQLite uses the FTS5 virtual table (slot 0020) instead of a
-- generated column. Parity placeholder.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
