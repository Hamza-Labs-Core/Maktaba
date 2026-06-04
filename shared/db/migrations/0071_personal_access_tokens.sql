-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0071 (web-pages-batch2 / Profile API Tokens) —
-- `personal_access_tokens` (PATs).
--
-- A PAT lets a user authenticate API calls with a long-lived bearer
-- token (`Authorization: Bearer pat_<prefix>_<secret>`) instead of a
-- cookie/JWT — for scripts, CI, and the CLI. Security model:
--
--   * `prefix`     the public, indexed lookup key (the `<prefix>` slug
--                  in the token). UNIQUE so a presented token resolves
--                  to exactly one row in O(1) before any hashing.
--   * `token_hash` SHA-256 of the SECRET half only. The raw token is
--                  shown to the user exactly once at creation; the
--                  server keeps only the hash, so a DB read can't replay
--                  a token. Verify is a constant-time compare of the
--                  recomputed hash.
--   * `scopes`     coarse capability set (TEXT[]). Empty ⇒ inherits the
--                  owner's full permissions (v1 default).
--   * `expires_at` optional hard expiry; `revoked_at` is the manual
--                  kill-switch. Either one set + past ⇒ token rejected.
--   * `last_used_at` is best-effort touched on each successful auth so
--                  the UI can show "last used".
--
CREATE TABLE IF NOT EXISTS personal_access_tokens (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT         NOT NULL,
    token_hash    TEXT         NOT NULL,
    prefix        TEXT         NOT NULL,
    scopes        TEXT[]       NOT NULL DEFAULT '{}',
    last_used_at  TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS personal_access_tokens_prefix
    ON personal_access_tokens (prefix);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS personal_access_tokens_user_active
    ON personal_access_tokens (user_id) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS personal_access_tokens_user_active;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS personal_access_tokens_prefix;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS personal_access_tokens;
-- +goose StatementEnd
