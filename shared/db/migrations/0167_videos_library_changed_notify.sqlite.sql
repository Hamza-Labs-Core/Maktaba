-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0167 (the browse.library_changed NOTIFY).
--
-- SQLite has no LISTEN/NOTIFY, so the Postgres trigger has no SQL-level
-- equivalent here; an API on SQLite keeps no rows in memory (its row cache is
-- wired only with the Postgres listener). This file keeps the slot occupied so
-- the migration runner stays in lock-step on both backends.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
