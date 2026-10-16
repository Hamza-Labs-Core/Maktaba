-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0091 (plan 06-11 section 10.5).
--
-- Drops the two legacy live-row unique indexes once processing_jobs_enqueue()
-- semantics (engine/enqueue.py on SQLite) are the only INSERT path. See the
-- Postgres file for the merge-order rule.
--
DROP INDEX IF EXISTS processing_jobs_one_live_per_video_stage;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS processing_jobs_one_live_scan_per_library;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS processing_jobs_one_live_per_video_stage
    ON processing_jobs (video_id, stage)
    WHERE state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS processing_jobs_one_live_scan_per_library
    ON processing_jobs (library_id, stage)
    WHERE stage = 'scan'
      AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused');
-- +goose StatementEnd
