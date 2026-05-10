-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id            TEXT    PRIMARY KEY,
    user_id       TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hash          TEXT    NOT NULL,
    family_id     TEXT    NOT NULL,
    device_id     TEXT    REFERENCES devices(id) ON DELETE CASCADE,
    issued_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at    TEXT    NOT NULL,
    revoked_at    TEXT,
    replaced_by   TEXT    REFERENCES refresh_tokens(id),
    client_meta   TEXT    NOT NULL DEFAULT '{}'
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS refresh_tokens_user_active
    ON refresh_tokens (user_id, family_id) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS refresh_tokens_family
    ON refresh_tokens (family_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS refresh_tokens_device
    ON refresh_tokens (device_id) WHERE device_id IS NOT NULL AND revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS refresh_tokens_reaper
    ON refresh_tokens (expires_at) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS refresh_tokens_reaper;
DROP INDEX IF EXISTS refresh_tokens_device;
DROP INDEX IF EXISTS refresh_tokens_family;
DROP INDEX IF EXISTS refresh_tokens_user_active;
DROP TABLE IF EXISTS refresh_tokens;
-- +goose StatementEnd
