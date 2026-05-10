-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0054 (Story 21.6 / plan-21-06) — canonical `audit_log` table.
--
-- Earlier epics (09, 12, 19, 23, 24) all wrote to a presumed `audit_log`
-- without a single shape; PLAN_REVIEW_18_24 §1.4 flagged the drift.
-- This migration is the sole creator. Columns and category enum match
-- plan-21-06 plus the `device` category Epic 12 needs.
--
CREATE TABLE IF NOT EXISTS audit_log (
    id            UUID         PRIMARY KEY,
    occurred_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
    category      TEXT         NOT NULL CHECK (category IN (
                                'auth', 'library', 'admin', 'data',
                                'config', 'keys', 'device', 'security',
                                'integrity', 'subscription')),
    action        TEXT         NOT NULL,
    actor_user    UUID,
    actor_ip      INET,
    actor_source  TEXT,
    target_kind   TEXT,
    target_id     TEXT,
    payload       JSONB        NOT NULL DEFAULT '{}'::jsonb,
    error_id      TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_log_occurred_at
    ON audit_log (occurred_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_log_actor_user
    ON audit_log (actor_user, occurred_at DESC)
    WHERE actor_user IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_log_category
    ON audit_log (category, occurred_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_log_target
    ON audit_log (target_kind, target_id, occurred_at DESC)
    WHERE target_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS audit_log_target;
DROP INDEX IF EXISTS audit_log_category;
DROP INDEX IF EXISTS audit_log_actor_user;
DROP INDEX IF EXISTS audit_log_occurred_at;
DROP TABLE IF EXISTS audit_log;
-- +goose StatementEnd
