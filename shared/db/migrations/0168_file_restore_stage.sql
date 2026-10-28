-- +goose Up
-- +goose StatementBegin
--
-- Slot 0168 (Epic 47, story 47.11) - Restore from the trash is a queue job.
--
--   job_stages  file_restore (library subject, pool nas_write, media lane 18): undoes
--               the file_actions rows its payload lists ({"action_ids": [...]}) through
--               library_mgmt/swap.py restore. The API only queues it (the file-actions
--               page's Restore), so no file is moved outside a worker.
--
-- Additive: one new row.
--
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('file_restore', 'Restore from trash', ARRAY['library']::text[], 'nas_write', 'media', 18, 1, 0, 0, 2, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, true, 'auto', 'any', true, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'file_restore';
-- +goose StatementEnd
