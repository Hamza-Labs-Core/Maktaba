-- +goose Up
-- +goose StatementBegin
--
-- Slot 0105 (parity with mediaintel check-tags / check-clips / books) - register the three
-- stages and the pool they share. check_tags and books (a season) and check_clips (a
-- library) follow tag: the language model's and the book's corrections are written over the
-- keyword tags, so a new tag run applies them again from their cached answers (slots 0102,
-- 0103) before a clip group or a channel reads the tags. tag 6 also tags each episode by its
-- own title and chapter playlist (slot 0104), reads the detection settings and strips the
-- channel's branding; the version sweep re-tags once. Workers re-sync job_stages from the
-- graph at start (engine/sync.py); this seed keeps a fresh database consistent first.
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
    ('check_tags', 'Check series tags', ARRAY['season']::text[], 'lookup', 'seasons', 3, 1, 0, 0, 3, '[{"to":"books","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['season']::text[], false, false, false, 'auto'),
    ('check_clips', 'Check clip tags', ARRAY['library']::text[], 'lookup', 'seasons', 3, 2, 30, 300, 3, '[{"to":"clip_groups","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['library']::text[], false, false, false, 'auto'),
    ('books', 'Books', ARRAY['season']::text[], 'lookup', 'seasons', 4, 1, 0, 0, 3, '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['season']::text[], false, false, false, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"check_tags","map":"same","when":"output_changed","legacy":false},{"to":"check_clips","map":"library_of","when":"output_changed","legacy":false}]'::jsonb, version = 6 WHERE stage = 'tag';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"clip_groups","map":"library_of","when":"output_changed","legacy":false}]'::jsonb, version = 5 WHERE stage = 'tag';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('check_tags', 'check_clips', 'books')
   AND NOT EXISTS (SELECT 1 FROM processing_jobs WHERE stage IN ('check_tags', 'check_clips', 'books'));
-- +goose StatementEnd
