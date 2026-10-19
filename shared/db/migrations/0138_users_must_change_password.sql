-- +goose Up
-- +goose StatementBegin
--
-- Slot 0138 (admin password reset) — there is no mailer, so a forgotten
-- password is reset by an admin on the Users page. The admin either sets a
-- temporary password, which the user must replace at the next sign-in, or
-- types the new password. `must_change_password` is that "replace it"
-- flag: while it is true the session can only reach the change-password
-- form.
--
-- The self-service "Forgot password" flow is gone with it. Its tokens
-- (slot 0070) were written to the server log in plain text, so any that
-- are still unused are deleted here. The table stays (0103 grants refer to
-- it) and nothing writes to it any more.
--
-- ADD COLUMN ... DEFAULT false is metadata-only on PostgreSQL 11+ (no
-- table rewrite), so it is safe on a non-empty `users`.
--
ALTER TABLE users ADD COLUMN IF NOT EXISTS must_change_password BOOLEAN NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM password_reset_tokens;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE users DROP COLUMN IF EXISTS must_change_password;
-- +goose StatementEnd
