-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0070 (web-pages-batch2 / Forgot Password) —
-- `password_reset_tokens`.
--
-- The forgot-password flow mints a single-use, time-boxed token, emails
-- it to the user (delivery is out of scope here), and the reset-password
-- flow exchanges it for a password change. Only the SHA-256 of the
-- token is persisted (`token_hash`) so a database read cannot reconstruct
-- a live reset link — the plaintext exists only in the email/response.
-- SHA-256 (not argon2id) because the reset endpoint must look the row up
-- BY hash; the token itself carries 256 bits of entropy, so a fast hash
-- is appropriate.
--
--   * `expires_at` bounds the window (handler uses ~1h).
--   * `used_at`    enforces single-use (set on a successful reset).
--   * the partial index serves the hot "find a live token" lookup.
--
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT         NOT NULL,
    expires_at  TIMESTAMPTZ  NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS password_reset_tokens_hash
    ON password_reset_tokens (token_hash);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS password_reset_tokens_user
    ON password_reset_tokens (user_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS password_reset_tokens_user;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS password_reset_tokens_hash;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS password_reset_tokens;
-- +goose StatementEnd
