-- +goose Up
-- +goose StatementBegin
--
-- Slot 0093 (plan 06-11 section 18) - register the thumb_small and review_pages
-- queues and their pool. Workers re-sync job_stages from the graph at start
-- (engine/sync.py); this seed keeps a fresh database consistent before the first
-- worker runs, as slot 0087 does for the original stages.
--
-- reports: review pages only, one at a time, so a slow page never takes a cpu
-- or network slot from the stages that write to Plex and dizqueTV.
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
    ('thumb_small', 'Small thumbnail', ARRAY['video']::text[], 'nas_read', 'episodes', 6, 1, 0, 0, 3, '[{"to":"review_pages","map":"library_of","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['video']::text[], false, false, false, 'auto'),
    ('review_pages', 'Review pages', ARRAY['library']::text[], 'reports', 'seasons', 6, 1, 120, 900, 3, '[]'::jsonb, '{}'::text[], ARRAY['library']::text[], false, false, false, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false},{"to":"thumb_small","map":"same","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'thumb_hash';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"review_pages","map":"same","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"thumb_small","map":"same","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'thumbnail';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"detect","map":"library_of","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'thumb_hash';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[]'::jsonb WHERE stage = 'thumbnail';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('thumb_small', 'review_pages');
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM worker_pools WHERE pool = 'reports';
-- +goose StatementEnd
