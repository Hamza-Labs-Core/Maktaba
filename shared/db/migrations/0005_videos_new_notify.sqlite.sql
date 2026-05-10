-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0005 (Story 1.1 / plan-01-01).
--
-- SQLite has no LISTEN/NOTIFY, so the Postgres trigger has no SQL-level
-- equivalent here. The Python scanner publishes `videos.new` events on
-- the in-process PubsubBus after a successful INSERT commits — the same
-- pattern slot 0002 uses for `jobs.new`. This file exists only to keep
-- the slot occupied so the migration runner stays in lock-step on
-- both backends.
--
-- The no-op SELECT keeps goose happy (it expects at least one statement
-- in the Up section) and is safe to re-apply.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
