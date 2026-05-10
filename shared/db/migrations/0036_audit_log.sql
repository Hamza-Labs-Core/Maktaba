-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0036 (Epic 7 / cross-cutting) — append-only audit log.
--
-- Used by destructive endpoints (Story 7.3 ?purge, Story 7.4 ?purge,
-- speaker merges, settings patches) to record actor + payload. ``category``
-- carves the log into facets (``library``, ``settings``, ``auth``…)
-- without proliferating tables.
--
CREATE TABLE IF NOT EXISTS audit_log (
    id            BIGSERIAL    PRIMARY KEY,
    category      TEXT         NOT NULL,
    action        TEXT         NOT NULL,
    actor_user_id UUID         REFERENCES users(id) ON DELETE SET NULL,
    target_id     TEXT,
    payload       JSONB        NOT NULL DEFAULT '{}'::jsonb,
    ts            TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_log_actor_idx
    ON audit_log (actor_user_id, ts DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS audit_log_category_idx
    ON audit_log (category, action, ts DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS audit_log_category_idx;
DROP INDEX IF EXISTS audit_log_actor_idx;
DROP TABLE IF EXISTS audit_log;
-- +goose StatementEnd
