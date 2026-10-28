-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0169 (the job_stages seed matches the graph for what
-- the enqueue reads: subject_kinds, enabled; and the graph's versions).
--
UPDATE job_stages SET subject_kinds = '["library","video"]'
 WHERE stage IN ('convert_mp4', 'dedup_scan');
UPDATE job_stages SET enabled = 1, disabled_reason = NULL
 WHERE stage IN ('convert_mp4', 'dedup_scan', 'dedup_apply', 'youtube_fetch', 'youtube_backfill')
   AND disabled_reason = 'not_configured';
UPDATE job_stages SET version = 3 WHERE stage = 'display_title' AND version < 3;
UPDATE job_stages SET version = 4 WHERE stage = 'write_channels' AND version < 4;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET subject_kinds = '["video"]' WHERE stage = 'convert_mp4';
UPDATE job_stages SET subject_kinds = '["library"]' WHERE stage = 'dedup_scan';
-- +goose StatementEnd
