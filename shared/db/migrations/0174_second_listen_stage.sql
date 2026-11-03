-- +goose Up
-- +goose StatementBegin
--
-- Slot 0174 - The second listen is a stage of its own.
--
--   worker_pools  listen (per worker, 4): the second listen's batch on a machine.
--   job_stages    second_listen (video subject, pool listen, speech lane 3): Cohere
--                 Transcribe Arabic on Whisper's stored transcript (its words) and the
--                 file's audio; subtitle_gen, index and tag follow it, not transcribe.
--                 The later speech stages move one place down the lane.
--
-- Additive: one new pool, one new stage, five lane orders. The edges are the
-- pipeline's to write (its graph sync), as for every stage.
--
INSERT INTO worker_pools (pool, label, scope, size, default_size)
VALUES ('listen', 'Second listen', 'per_worker', 4, 4)
ON CONFLICT (pool) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET lane_order = CASE stage
        WHEN 'subtitle_gen' THEN 4
        WHEN 'index' THEN 5
        WHEN 'chapter_infer' THEN 6
        WHEN 'chapter_thumbs' THEN 7
        WHEN 'topic_assign' THEN 8
        WHEN 'topic_recluster' THEN 9
    END
WHERE stage IN ('subtitle_gen', 'index', 'chapter_infer', 'chapter_thumbs', 'topic_assign',
                'topic_recluster');
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('second_listen', 'Second listen', ARRAY['video']::text[], 'listen', 'speech', 3, 1, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'any', true, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'second_listen';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET lane_order = CASE stage
        WHEN 'subtitle_gen' THEN 3
        WHEN 'index' THEN 4
        WHEN 'chapter_infer' THEN 5
        WHEN 'chapter_thumbs' THEN 6
        WHEN 'topic_assign' THEN 7
        WHEN 'topic_recluster' THEN 8
    END
WHERE stage IN ('subtitle_gen', 'index', 'chapter_infer', 'chapter_thumbs', 'topic_assign',
                'topic_recluster');
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM worker_pools WHERE pool = 'listen';
-- +goose StatementEnd
