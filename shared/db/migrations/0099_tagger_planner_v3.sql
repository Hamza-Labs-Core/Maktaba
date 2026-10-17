-- +goose Up
-- +goose StatementBegin
--
-- Slot 0099 (plan 06-11 section 17.7) - PR #52: the tagger (v3: sub-topics, per-episode
-- tags, khutbah and «بصوت» fixes) and the 20-channel lineup planner (v3). The seeded
-- rows carry the new versions so a fresh database matches the graph before the first
-- worker syncs it; the version sweep then re-tags and re-plans once.
--
UPDATE job_stages SET version = 3 WHERE stage = 'tag';
UPDATE job_stages SET version = 3 WHERE stage = 'plan_channels';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 1 WHERE stage = 'tag';
UPDATE job_stages SET version = 2 WHERE stage = 'plan_channels';
-- +goose StatementEnd
