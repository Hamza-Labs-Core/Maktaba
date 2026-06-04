-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0068 (web-pages-batch2 / Register + Account) — extend `users`
-- with self-service profile fields.
--
-- Story 10.1 shipped `users` with username/pw_hash only; the web
-- Register flow collects an email at sign-up and the Account page lets
-- a user edit their display name + email. Both columns are nullable so
-- the pre-existing rows (including the sentinel admin seeded in 0029)
-- migrate cleanly with no backfill.
--
--   * `email`        optional contact + the lookup key for the
--                    forgot-password flow (slot 0070). Case-insensitively
--                    unique via a partial UNIQUE index so two accounts
--                    can't claim the same address, while the many NULLs
--                    (legacy rows) don't collide.
--   * `display_name` optional human label shown in the UI; falls back to
--                    `username` when empty.
--
-- Why NO TRANSACTION: `CREATE INDEX CONCURRENTLY` cannot run inside a
-- transaction. ADD COLUMN IF NOT EXISTS is backfill-safe on a non-empty
-- table (metadata-only, no rewrite).
--
ALTER TABLE users ADD COLUMN IF NOT EXISTS email TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS users_email_lower_unique
    ON users (lower(email)) WHERE email IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS users_email_lower_unique;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS display_name;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS email;
-- +goose StatementEnd
