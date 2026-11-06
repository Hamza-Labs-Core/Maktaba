-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0184 (processing_done_subjects: the lock order checked
-- across a transaction). SQLite takes one lock for the whole database, so no two writers
-- wait on each other in a circle: nothing to check. This file keeps the slot occupied so
-- the migration runner stays in lock-step on both backends; the no-op SELECT keeps goose
-- happy and is safe to re-apply.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
