-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0138: the admin-reset "choose a new
-- password at the next sign-in" flag (BOOLEAN becomes INTEGER 0/1), and
-- the self-service reset tokens of slot 0070, which were logged in plain
-- text, are deleted.
--
ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM password_reset_tokens;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN must_change_password;
-- +goose StatementEnd
