-- +goose Up
-- +goose StatementBegin
-- Slot 0234 (Epic 49 story 49.7): Postgres's documents get their normalised text as
-- columns for its BM25 indexes. SQLite has none of those: its FTS5 table already holds the
-- normalised text (slot 0232), so there is nothing to add.
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
