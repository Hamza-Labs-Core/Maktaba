-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0068. SQLite has no partial UNIQUE
-- index on an expression with the same ergonomics as Postgres, but it
-- DOES support `CREATE UNIQUE INDEX ... WHERE`, so the case-insensitive
-- partial-unique contract is preserved with lower(email).
--
ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_unique
    ON users (lower(email)) WHERE email IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS users_email_lower_unique;
-- +goose StatementEnd
