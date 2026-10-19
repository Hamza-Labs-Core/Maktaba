-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0109: arrays and JSONB are JSON text, booleans are 0/1.
--
INSERT INTO worker_pools (pool, label, scope, size, default_size) VALUES
    ('lookup', 'Model and encyclopedia', 'global', 1, 1)
ON CONFLICT (pool) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('check_tags', 'Check series tags', '["season"]', 'lookup', 'seasons', 3, 1, 0, 0, 3, '[{"to":"books","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]', '[]', '["season"]', 0, 0, 0, 'auto'),
    ('check_clips', 'Check clip tags', '["library"]', 'lookup', 'seasons', 3, 2, 30, 300, 3, '[{"to":"clip_groups","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]', '[]', '["library"]', 0, 0, 0, 'auto'),
    ('books', 'Books', '["season"]', 'lookup', 'seasons', 4, 1, 0, 0, 3, '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]', '[]', '["season"]', 0, 0, 0, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"check_tags","map":"same","when":"output_changed","legacy":false},{"to":"check_clips","map":"library_of","when":"output_changed","legacy":false}]', version = 6 WHERE stage = 'tag';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"clip_groups","map":"library_of","when":"output_changed","legacy":false}]', version = 5 WHERE stage = 'tag';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('check_tags', 'check_clips', 'books')
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage IN ('check_tags', 'check_clips', 'books'));
-- +goose StatementEnd
