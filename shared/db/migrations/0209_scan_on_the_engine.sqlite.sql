-- +goose Up
-- +goose StatementBegin
--
-- Slot 0209 - Epic 54 story 54.36 §2: `scan` runs on the engine, no longer as a legacy
-- handler. The seed says so (job_stages.legacy), as slot 0100 did for transcribe, so a freshly
-- migrated database matches the graph before a worker's graph sync (engine/sync.py) rewrites
-- the row; the API's queue list reads the column. Data only: no table, column or function.
--
UPDATE job_stages SET legacy = 0 WHERE stage = 'scan';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET legacy = 1 WHERE stage = 'scan';
-- +goose StatementEnd
