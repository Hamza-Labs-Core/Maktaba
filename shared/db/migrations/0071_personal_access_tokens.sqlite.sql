-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0071. SQLite has no array type, so
-- `scopes` is a JSON-encoded TEXT array (the Go layer marshals/unmarshals
-- it); the Postgres side uses a native TEXT[].
--
CREATE TABLE IF NOT EXISTS personal_access_tokens (
    id            TEXT    PRIMARY KEY,
    user_id       TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT    NOT NULL,
    token_hash    TEXT    NOT NULL,
    prefix        TEXT    NOT NULL,
    scopes        TEXT    NOT NULL DEFAULT '[]',
    last_used_at  TEXT,
    expires_at    TEXT,
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    revoked_at    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS personal_access_tokens_prefix
    ON personal_access_tokens (prefix);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS personal_access_tokens_user_active
    ON personal_access_tokens (user_id) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS personal_access_tokens_user_active;
DROP INDEX IF EXISTS personal_access_tokens_prefix;
DROP TABLE IF EXISTS personal_access_tokens;
-- +goose StatementEnd
