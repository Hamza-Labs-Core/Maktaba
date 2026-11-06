-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0186 (the mediaintel import's dry run writes no
-- done-subjects summary). SQLite's summary triggers take one database lock, which a
-- dry run holds anyway: nothing to change. This file keeps the slot occupied so the
-- migration runner stays in lock-step on both backends.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
