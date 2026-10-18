-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0106 (plan 06-11, mediaintel parity): arrays and JSONB are
-- JSON text, booleans are 0/1.
--
INSERT INTO worker_pools (pool, label, scope, size, default_size) VALUES
    ('audits', 'Channel audit', 'global', 1, 1),
    ('repairs', 'Repairs', 'global', 1, 1)
ON CONFLICT (pool) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('exact_dates', 'Exact dates', '["library"]', 'network_yt', 'sources', 1, 1, 300, 3600, 3, '[{"to":"detect","map":"same","when":"output_changed","legacy":false}]', '[]', '["library"]', 0, 0, 0, 'auto'),
    ('repair_broken', 'Repair broken files', '["library"]', 'repairs', 'media', 10, 1, 0, 0, 1, '[]', '["probe","content_hash"]', '[]', 0, 0, 1, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"link","map":"videos_of","when":"output_changed","legacy":false},{"to":"detect","map":"same","when":"output_changed","legacy":false},{"to":"exact_dates","map":"same","when":"output_changed","legacy":false}]' WHERE stage = 'youtube_sync';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"link","map":"videos_of","when":"output_changed","legacy":false},{"to":"detect","map":"same","when":"output_changed","legacy":false}]' WHERE stage = 'youtube_sync';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('exact_dates', 'repair_broken')
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage IN ('exact_dates', 'repair_broken'));
-- +goose StatementEnd
