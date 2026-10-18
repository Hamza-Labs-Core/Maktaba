-- +goose Up
-- +goose StatementBegin
--
-- Slot 0100 (plan 03-10) - transcription on the queue engine. transcribe (gpu pool, a
-- batched Whisper per worker) and subtitle_gen (files next to the video, nas_write pool)
-- stop being legacy stages; content_hash leads to transcribe, so a new video is
-- transcribed after its identity is read. The gpu pool's size is the batch (32).
-- Workers re-sync job_stages from the graph at start; this keeps a fresh database
-- consistent first.
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
    version = 1, legacy = false, max_attempts = 3,
    edges = '[{"to":"subtitle_gen","map":"same","when":"output_changed","legacy":false},{"to":"index","map":"same","when":"output_changed","legacy":true}]'::jsonb
  WHERE stage = 'transcribe';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET label = 'Subtitle files', pool = 'nas_write', lane = 'media',
    lane_order = 3, version = 1, legacy = false, external_write = true, edges = '[]'::jsonb
  WHERE stage = 'subtitle_gen';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET
    edges = '[{"to":"thumb_small","map":"same","when":"output_changed","legacy":false},{"to":"transcribe","map":"same","when":"output_changed","legacy":false}]'::jsonb
  WHERE stage = 'content_hash';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET pool = 'gpu', lane = 'media', lane_order = 2, legacy = true,
    edges = '[{"to":"subtitle_gen","map":"same","when":"output_changed","legacy":true},{"to":"index","map":"same","when":"output_changed","legacy":true}]'::jsonb
  WHERE stage = 'transcribe';
UPDATE job_stages SET label = 'Subtitles', pool = 'cpu', lane_order = 3, legacy = true,
    external_write = false WHERE stage = 'subtitle_gen';
UPDATE job_stages SET
    edges = '[{"to":"thumb_small","map":"same","when":"output_changed","legacy":false}]'::jsonb
  WHERE stage = 'content_hash';
UPDATE worker_pools SET size = 1, default_size = 1 WHERE pool = 'gpu';
DELETE FROM worker_pools WHERE pool = 'nas_write';
-- +goose StatementEnd
