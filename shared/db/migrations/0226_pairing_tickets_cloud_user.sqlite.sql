-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0226 (Epic 58, story 58.03): the cloud account that
-- approved a TV's code. See the Postgres file.
--
ALTER TABLE pairing_tickets ADD COLUMN IF NOT EXISTS cloud_user_id TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
