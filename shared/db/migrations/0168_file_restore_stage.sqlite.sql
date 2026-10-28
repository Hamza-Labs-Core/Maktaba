-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0168 (Epic 47: Restore from the trash is the
-- file_restore queue stage).
--
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('file_restore', 'Restore from trash', '["library"]', 'nas_write', 'media', 18, 1, 0, 0, 2, '[]', '[]', '[]', 0, 0, 1, 'auto', 'any', 1, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'file_restore';
-- +goose StatementEnd
