-- +goose Up
-- +goose StatementBegin
-- Two-column (user_id, idem_key) composite PK — mirrors the Postgres
-- sibling. The single-column NUL-joined key the first cut used is
-- unstorable on Postgres (TEXT rejects 0x00); SQLite tolerates it but
-- parity with the production engine is the rule, so both use the
-- collision-free two-column key.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    user_id        TEXT     NOT NULL DEFAULT '',
    idem_key       TEXT     NOT NULL,
    request_hash   TEXT     NOT NULL,
    status         INTEGER  NOT NULL,
    body           BLOB     NOT NULL,
    headers        TEXT     NOT NULL DEFAULT '{}',
    created_at     TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (user_id, idem_key)
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
