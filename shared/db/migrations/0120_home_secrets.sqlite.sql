-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0120 (Epic 32, story 32.6): home_secrets, the home's
-- sealed secrets (AES-256-GCM under the key file secrets.key in the home's config
-- directory; the key never enters the database). Same columns as the Postgres slot. SQLite
-- has no roles (a SQLite home takes no machines), so there is nothing to revoke. Timestamps
-- are ISO-8601 UTC text.
--
CREATE TABLE IF NOT EXISTS home_secrets (
    name        TEXT PRIMARY KEY,
    ciphertext  BLOB NOT NULL,
    nonce       BLOB NOT NULL,
    key_id      TEXT NOT NULL,
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_by  TEXT
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS home_secrets;
-- +goose StatementEnd
