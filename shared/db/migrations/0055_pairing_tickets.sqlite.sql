-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS pairing_tickets (
    code         TEXT    PRIMARY KEY,
    user_id      TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issued_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at   TEXT    NOT NULL,
    consumed_at  TEXT,
    consumed_by  TEXT    REFERENCES devices(id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS pairing_tickets_user
    ON pairing_tickets (user_id, issued_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS pairing_tickets_reaper
    ON pairing_tickets (expires_at) WHERE consumed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS pairing_tickets_reaper;
DROP INDEX IF EXISTS pairing_tickets_user;
DROP TABLE IF EXISTS pairing_tickets;
-- +goose StatementEnd
