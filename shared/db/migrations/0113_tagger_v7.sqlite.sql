-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0113 (plan 06-11 section 3.2, plan 03-10).
--
UPDATE job_stages SET version = 7 WHERE stage = 'tag';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 6 WHERE stage = 'tag';
-- +goose StatementEnd
