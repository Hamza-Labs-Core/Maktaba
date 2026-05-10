-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS web_sessions (
    id            TEXT     PRIMARY KEY,
    user_id       TEXT     NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_token    TEXT     NOT NULL,
    created_at    TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at  TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at    TEXT     NOT NULL,
    ip            TEXT,
    user_agent    TEXT,
    revoked_at    TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS web_sessions_user_active
    ON web_sessions (user_id) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS web_sessions_reaper
    ON web_sessions (expires_at) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS web_sessions_reaper;
DROP INDEX IF EXISTS web_sessions_user_active;
DROP TABLE IF EXISTS web_sessions;
-- +goose StatementEnd
