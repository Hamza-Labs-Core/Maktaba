-- +goose Up
-- +goose StatementBegin
-- SQLite FTS5 indexes are built into the virtual table from slot 0020.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
