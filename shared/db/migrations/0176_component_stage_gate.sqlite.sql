-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0176 (Epic 51, story 51.06: a stage switched off by its
-- component makes no job, one held by it still queues; the catch-up payload's
-- "components" merge). SQLite has no processing_jobs_enqueue() or
-- processing_jobs_payload_merge(): engine/enqueue.py carries the same rules. This file
-- keeps the slot occupied so the migration runner stays in lock-step on both backends;
-- the no-op SELECT keeps goose happy and is safe to re-apply.
--
SELECT 1;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
