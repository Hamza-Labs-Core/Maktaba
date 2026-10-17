-- +goose Up
-- +goose StatementBegin
--
-- Slot 0095 (plan 06-11 section 17.7) - the channel planner is now the TV slot
-- grid (feat/tv-grid, PR #50), planner version 2. The seeded plan_channels row
-- carries the new version so a fresh database matches the graph before the first
-- worker syncs it; the version sweep then re-plans the catalog once.
--
UPDATE job_stages SET version = 2 WHERE stage = 'plan_channels';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 1 WHERE stage = 'plan_channels';
-- +goose StatementEnd
