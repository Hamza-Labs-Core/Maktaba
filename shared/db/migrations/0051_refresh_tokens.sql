-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0051 (Story 10.3 / plan-10-03) — `refresh_tokens` table for the
-- native (JWT + refresh) login flow.
--
-- Schema mirrors README.md §"refresh_tokens". Notes:
--   * `hash` is argon2id of the SECRET HALF only of the opaque token
--     `mkt_rt_v1.<id>.<secret>` (plan-10-03 §5). Plaintext is returned
--     only at issue time.
--   * `family_id` is shared across a rotation chain — used by reuse
--     detection (Story 10.4 AC-2).
--   * `device_id` is nullable so we can land this table without `devices`
--     coupling — registration backfills it via FK update once devices
--     exist (slot 0040).
--
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id            UUID         PRIMARY KEY,
    user_id       UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hash          TEXT         NOT NULL,
    family_id     UUID         NOT NULL,
    device_id     UUID         REFERENCES devices(id) ON DELETE CASCADE,
    issued_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ  NOT NULL,
    revoked_at    TIMESTAMPTZ,
    replaced_by   UUID         REFERENCES refresh_tokens(id),
    client_meta   JSONB        NOT NULL DEFAULT '{}'::jsonb
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS refresh_tokens_user_active
    ON refresh_tokens (user_id, family_id) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS refresh_tokens_family
    ON refresh_tokens (family_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS refresh_tokens_device
    ON refresh_tokens (device_id) WHERE device_id IS NOT NULL AND revoked_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS refresh_tokens_reaper
    ON refresh_tokens (expires_at) WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS refresh_tokens_reaper;
DROP INDEX IF EXISTS refresh_tokens_device;
DROP INDEX IF EXISTS refresh_tokens_family;
DROP INDEX IF EXISTS refresh_tokens_user_active;
DROP TABLE IF EXISTS refresh_tokens;
-- +goose StatementEnd
