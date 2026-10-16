-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0093 (plan 06-11 section 18): arrays and JSONB
-- are JSON text, booleans are 0/1.
--
INSERT INTO worker_pools (pool, label, scope, size, default_size) VALUES
    ('reports', 'Review pages', 'global', 1, 1)
ON CONFLICT (pool) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('thumb_small', 'Small thumbnail', '["video"]', 'nas_read', 'episodes', 6, 1, 0, 0, 3, '[{"to":"review_pages","map":"library_of","when":"output_changed","legacy":false}]', '[]', '["video"]', 0, 0, 0, 'auto'),
    ('review_pages', 'Review pages', '["library"]', 'reports', 'seasons', 6, 1, 120, 900, 3, '[]', '[]', '["library"]', 0, 0, 0, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false},{"to":"thumb_small","map":"same","when":"output_changed","legacy":false}]' WHERE stage = 'thumb_hash';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"review_pages","map":"same","when":"output_changed","legacy":false}]' WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"thumb_small","map":"same","when":"output_changed","legacy":false}]' WHERE stage = 'thumbnail';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false}]' WHERE stage = 'thumb_hash';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]' WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[]' WHERE stage = 'thumbnail';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('thumb_small', 'review_pages');
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM worker_pools WHERE pool = 'reports';
-- +goose StatementEnd
