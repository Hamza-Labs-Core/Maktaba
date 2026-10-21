-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0144 (a forced enqueue runs now). SQLite has no
-- processing_jobs_enqueue(): engine/enqueue.py carries the same rule (a forced trigger
-- takes over a pending job, or one given back for later, and clears its not_before).
-- This file keeps the slot occupied so the migration runner stays in lock-step on both
-- backends; the no-op SELECT keeps goose happy and is safe to re-apply.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
