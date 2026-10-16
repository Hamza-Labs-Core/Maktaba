-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0095 (plan 06-11 section 17.7).
--
UPDATE job_stages SET version = 2 WHERE stage = 'plan_channels';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 1 WHERE stage = 'plan_channels';
-- +goose StatementEnd
