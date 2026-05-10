-- +goose Up
-- +goose StatementBegin
-- Postgres has no FTS5 virtual table; the equivalent is the
-- `transcript_units.tsv` generated column (slot 0021) plus the GIN
-- index (slot 0023) and the `transcripts_fts` compatibility view
-- (slot 0024). This file is a parity placeholder.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
