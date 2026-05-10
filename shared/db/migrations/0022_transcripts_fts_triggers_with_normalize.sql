-- +goose Up
-- +goose StatementBegin
-- Postgres FTS sync is implicit via slot 0021's STORED generated column
-- — INSERT/UPDATE on `transcript_units` automatically updates `tsv`.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
