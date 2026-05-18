-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS idempotency_keys (
    composite_key  TEXT     PRIMARY KEY,
    user_id        TEXT     NOT NULL DEFAULT '',
    idem_key       TEXT     NOT NULL,
    request_hash   TEXT     NOT NULL,
    status         INTEGER  NOT NULL,
    body           BLOB     NOT NULL,
    headers        TEXT     NOT NULL DEFAULT '{}',
    created_at     TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS idempotency_keys_reaper
    ON idempotency_keys (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS idempotency_keys_reaper;
DROP TABLE IF EXISTS idempotency_keys;
-- +goose StatementEnd
