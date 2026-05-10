-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0029. Type substitutions per
-- migrations/README.md §3:
--   UUID         → TEXT
--   TIMESTAMPTZ  → TEXT (ISO-8601)
--   BOOLEAN      → INTEGER (0/1)
--
-- SQLite has no `lower()` index expression natively that matches the
-- Postgres semantics for unicode, but `lower()` works for ASCII which
-- is enough for v1 self-host. Application code casefolds before
-- INSERT/SELECT.
--
CREATE TABLE IF NOT EXISTS users (
    id               TEXT     PRIMARY KEY,
    username         TEXT     NOT NULL,
    pw_hash          TEXT     NOT NULL,
    is_admin         INTEGER  NOT NULL DEFAULT 0,
    failed_attempts  INTEGER  NOT NULL DEFAULT 0,
    locked_until     TEXT,
    created_at       TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS users_username_lower_unique
    ON users (lower(username));
-- +goose StatementEnd

-- +goose StatementBegin
INSERT OR IGNORE INTO users (id, username, pw_hash, is_admin)
    VALUES (
        '00000000-0000-0000-0000-000000000001',
        'admin',
        '<unsalted-disabled>',
        1
    );
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS users_username_lower_unique;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
