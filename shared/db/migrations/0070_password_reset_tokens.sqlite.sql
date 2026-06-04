-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0070.
--
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id          TEXT    PRIMARY KEY,
    user_id     TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT    NOT NULL,
    expires_at  TEXT    NOT NULL,
    used_at     TEXT,
    created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS password_reset_tokens_hash
    ON password_reset_tokens (token_hash);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS password_reset_tokens_user
    ON password_reset_tokens (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS password_reset_tokens_user;
DROP INDEX IF EXISTS password_reset_tokens_hash;
DROP TABLE IF EXISTS password_reset_tokens;
-- +goose StatementEnd
