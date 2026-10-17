-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0096 (plan 06-11, review of #47): the column that
-- keeps a mid-run "force cascade" for the follow-up (the enqueue logic lives in
-- engine/enqueue.py on SQLite).
--
ALTER TABLE processing_jobs ADD COLUMN IF NOT EXISTS rerun_force_cascade INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE processing_jobs DROP COLUMN IF EXISTS rerun_force_cascade;
-- +goose StatementEnd
