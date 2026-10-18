-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0100 (plan 03-10).
--
INSERT INTO worker_pools (pool, label, scope, size, default_size)
VALUES ('nas_write', 'NAS writes', 'global', 4, 4)
ON CONFLICT (pool) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE worker_pools SET size = 32, default_size = 32 WHERE pool = 'gpu';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET label = 'Transcribe', pool = 'gpu', lane = 'media', lane_order = 2,
    version = 1, legacy = 0, max_attempts = 3,
    edges = '[{"to":"subtitle_gen","map":"same","when":"output_changed","legacy":false},{"to":"index","map":"same","when":"output_changed","legacy":false},{"to":"tag","map":"season_of","when":"output_changed","legacy":false},{"to":"tag","map":"library_of","when":"output_changed","legacy":false}]'
  WHERE stage = 'transcribe';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET label = 'Subtitle files', pool = 'nas_write', lane = 'media',
    lane_order = 3, version = 1, legacy = 0, external_write = 1, edges = '[]'
  WHERE stage = 'subtitle_gen';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET label = 'Search index', pool = 'cpu', lane = 'media', lane_order = 4,
    version = 1, legacy = 0
  WHERE stage = 'index';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET version = 4, debounce_sec = 300, debounce_max_sec = 3600
  WHERE stage = 'tag';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET debounce_sec = 300, debounce_max_sec = 3600 WHERE stage = 'plan_channels';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET
    edges = '[{"to":"thumb_small","map":"same","when":"output_changed","legacy":false},{"to":"transcribe","map":"same","when":"output_changed","legacy":false}]'
  WHERE stage = 'content_hash';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET pool = 'gpu', lane = 'media', lane_order = 2, legacy = 1,
    edges = '[{"to":"subtitle_gen","map":"same","when":"output_changed","legacy":true},{"to":"index","map":"same","when":"output_changed","legacy":true}]'
  WHERE stage = 'transcribe';
UPDATE job_stages SET label = 'Subtitles', pool = 'cpu', lane_order = 3, legacy = 1,
    external_write = 0 WHERE stage = 'subtitle_gen';
UPDATE job_stages SET label = 'Index', legacy = 1 WHERE stage = 'index';
UPDATE job_stages SET version = 3, debounce_sec = 0, debounce_max_sec = 0 WHERE stage = 'tag';
UPDATE job_stages SET debounce_sec = 60, debounce_max_sec = 900 WHERE stage = 'plan_channels';
UPDATE job_stages SET
    edges = '[{"to":"thumb_small","map":"same","when":"output_changed","legacy":false}]'
  WHERE stage = 'content_hash';
UPDATE worker_pools SET size = 1, default_size = 1 WHERE pool = 'gpu';
DELETE FROM worker_pools WHERE pool = 'nas_write';
-- +goose StatementEnd
