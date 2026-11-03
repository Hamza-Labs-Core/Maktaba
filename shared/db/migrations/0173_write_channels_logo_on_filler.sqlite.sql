-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0173 (write_channels version 5: the corner logo on
-- filler by default).
--
UPDATE job_stages SET version = 5 WHERE stage = 'write_channels' AND version < 5;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 4 WHERE stage = 'write_channels' AND version = 5;
-- +goose StatementEnd
