-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0054 (Story 21.6 / plan-21-06) — canonical Epic-21 extension to
-- the slot-0036 `audit_log` table.
--
-- Slot 0036 created the table with id BIGSERIAL, category, action,
-- actor_user_id (UUID), target_id, payload, ts. This slot is purely
-- additive: it adds the columns, CHECK constraint, and read-path
-- indexes that Epic 21 (and the shared/log/go writer) need on top of
-- 0036, without conflicting with the pre-existing schema or callers.
--
-- All operations are idempotent so the migration is safe to re-run on
-- environments that have already applied it.
--
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS occurred_at TIMESTAMPTZ NOT NULL DEFAULT now();
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS actor_ip INET;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS actor_source TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS target_kind TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS error_id TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    DROP CONSTRAINT IF EXISTS audit_log_category_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD CONSTRAINT audit_log_category_check
    CHECK (category IN (
        'auth', 'library', 'admin', 'data', 'config',
        'keys', 'device', 'security', 'integrity', 'subscription'))
    NOT VALID;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_log_occurred_at
    ON audit_log (occurred_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_log_target
    ON audit_log (target_kind, target_id, occurred_at DESC)
    WHERE target_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS audit_log_target;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS audit_log_occurred_at;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log DROP CONSTRAINT IF EXISTS audit_log_category_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log DROP COLUMN IF EXISTS error_id;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log DROP COLUMN IF EXISTS target_kind;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log DROP COLUMN IF EXISTS actor_source;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log DROP COLUMN IF EXISTS actor_ip;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log DROP COLUMN IF EXISTS occurred_at;
-- +goose StatementEnd
