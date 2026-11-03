-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0174 (the second listen is a stage of its own:
-- the listen pool, the second_listen stage, the later speech stages' lane orders).
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
    ('second_listen', 'Second listen', '["video"]', 'listen', 'speech', 3, 1, 0, 0, 3, '[]', '[]', '[]', 0, 0, 0, 'auto', 'any', 1, NULL)
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
