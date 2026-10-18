-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0101 (plan 06-11 section 17.7).
--
UPDATE job_stages SET version = 5 WHERE stage = 'tag';
UPDATE job_stages SET version = 5 WHERE stage = 'plan_channels';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 4 WHERE stage = 'tag';
UPDATE job_stages SET version = 4 WHERE stage = 'plan_channels';
-- +goose StatementEnd
