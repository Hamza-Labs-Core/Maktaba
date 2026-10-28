-- +goose Up
-- +goose StatementBegin
--
-- Slot 0170 - the job_stages seed matches the graph again, for what SQL reads before a
-- worker's graph sync (engine/sync.py) rewrites the rows: processing_jobs_enqueue checks
-- a stage's subject_kinds and enabled, so on a freshly migrated database the API's
-- library-level "Convert to MP4" and "Find duplicates" were refused until a worker ran.
--
--   convert_mp4, dedup_scan    subject_kinds gain 'library' / 'video' (Epic 47.4, 47.7)
--   the five Epic 47 stages    on (they were seeded off, 'not_configured', until built);
--                              a stage an admin turned off keeps its state
--   display_title 3, write_channels 4   the graph's versions
--
-- Edges and may_request stay the sync's: only the pipeline reads them.
--
UPDATE job_stages SET subject_kinds = ARRAY['library','video']::text[]
 WHERE stage IN ('convert_mp4', 'dedup_scan');
UPDATE job_stages SET enabled = true, disabled_reason = NULL
 WHERE stage IN ('convert_mp4', 'dedup_scan', 'dedup_apply', 'youtube_fetch', 'youtube_backfill')
   AND disabled_reason = 'not_configured';
UPDATE job_stages SET version = 3 WHERE stage = 'display_title' AND version < 3;
UPDATE job_stages SET version = 4 WHERE stage = 'write_channels' AND version < 4;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET subject_kinds = ARRAY['video']::text[] WHERE stage = 'convert_mp4';
UPDATE job_stages SET subject_kinds = ARRAY['library']::text[] WHERE stage = 'dedup_scan';
-- +goose StatementEnd
