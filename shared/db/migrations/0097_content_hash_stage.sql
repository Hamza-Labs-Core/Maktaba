-- +goose Up
-- +goose StatementBegin
--
-- Slot 0097 (plan 06-11, mediaintel import) - register the content_hash stage: one job
-- per video that reads the file once (BLAKE3 of head + tail + size, as the scanner does)
-- in the nas_read pool. The import creates every video from the mediaintel cache with a
-- stand-in hash (videos.metadata.hash_pending) and queues this job; thumb_small, whose
-- cache folder is named after the hash, follows it. Workers re-sync job_stages from the
-- graph at start (engine/sync.py); this seed keeps a fresh database consistent first.
--
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('content_hash', 'File identity', ARRAY['video']::text[], 'nas_read', 'episodes', 0, 1, 0, 0, 3, '[{"to":"thumb_small","map":"same","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'content_hash'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage = 'content_hash');
-- +goose StatementEnd
