-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS devices (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform      TEXT NOT NULL CHECK (platform IN ('ios','android','web')),
    push_token    TEXT NOT NULL,
    bundle_id     TEXT NOT NULL,
    app_version   TEXT,
    locale        TEXT,
    registered_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at  TEXT NOT NULL DEFAULT (datetime('now')),
    revoked_at    TEXT,
    UNIQUE (user_id, platform, push_token)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS devices_user_active_idx
    ON devices (user_id) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS devices_user_active_idx;
DROP TABLE IF EXISTS devices;
-- +goose StatementEnd
