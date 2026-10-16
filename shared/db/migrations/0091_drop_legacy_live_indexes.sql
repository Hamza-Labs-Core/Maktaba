-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0091 (plan 06-11 section 10.5) - drop the two legacy live-row unique
-- indexes. Slot 0088 added processing_jobs_one_live_per_subject_stage, which
-- constrains every row they constrain, so they are redundant once every
-- INSERT goes through processing_jobs_enqueue().
--
-- MERGE ORDER: land this slot only after workstream A (db/jobs.py) and
-- workstream B (libraries/enqueuer.go) both call the function. Until then the
-- legacy indexes are what keeps an old INSERT path idempotent. Both
-- workstreams carry this item on their final PR checklist.
--
DROP INDEX CONCURRENTLY IF EXISTS processing_jobs_one_live_per_video_stage;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS processing_jobs_one_live_scan_per_library;
-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_one_live_per_video_stage
    ON processing_jobs (video_id, stage)
    WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS processing_jobs_one_live_scan_per_library
    ON processing_jobs (library_id, stage)
    WHERE stage = 'scan'
      AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd
