-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0029 (Story 10.1 / plan-10-01) — `users` table for authentication.
--
-- Owned by Epic 10 Story 10.1. The schema mirrors README.md §"users":
--   * `id` UUID, primary key. The fixed sentinel id
--     `00000000-0000-0000-0000-000000000001` is pre-seeded so the
--     single-user / admin-token bypass path (Story 10.9) and the audit
--     log have a real row to point at.
--   * `username` TEXT with case-insensitive uniqueness via a UNIQUE
--     index on `lower(username)`. Display preserves the original case.
--   * `pw_hash` is the standard PHC `$argon2id$...` string. The
--     sentinel admin gets the literal `<unsalted-disabled>` so the
--     argon2 verifier always rejects credential-style attacks against
--     the bypass row (Story 10.9 AC-1).
--   * `is_admin` boolean, false by default.
--   * `failed_attempts` and `locked_until` are added now (rather than
--     deferred to plan-10-11) so the brute-force counter has a column
--     to read/write from day one — Story 10.1 AC-3 references the
--     unlock endpoint that resets these.
--
-- Why NO TRANSACTION: `CREATE INDEX CONCURRENTLY` cannot run inside a
-- transaction; the rest of this file is naturally idempotent.
--
CREATE TABLE IF NOT EXISTS users (
    id               UUID         PRIMARY KEY,
    username         TEXT         NOT NULL,
    pw_hash          TEXT         NOT NULL,
    is_admin         BOOLEAN      NOT NULL DEFAULT false,
    failed_attempts  INT          NOT NULL DEFAULT 0,
    locked_until     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS users_username_lower_unique
    ON users (lower(username));
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO users (id, username, pw_hash, is_admin)
    VALUES (
        '00000000-0000-0000-0000-000000000001',
        'admin',
        '<unsalted-disabled>',
        true
    )
ON CONFLICT (id) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS users_username_lower_unique;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS users;
-- +goose StatementEnd
