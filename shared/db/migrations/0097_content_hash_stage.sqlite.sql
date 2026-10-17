-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0097 (plan 06-11, mediaintel import): arrays and
-- JSONB are JSON text, booleans are 0/1.
--
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('content_hash', 'File identity', '["video"]', 'nas_read', 'episodes', 0, 1, 0, 0, 3, '[{"to":"thumb_small","map":"same","when":"output_changed","legacy":false}]', '[]', '[]', 0, 0, 0, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'content_hash'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage = 'content_hash');
-- +goose StatementEnd
