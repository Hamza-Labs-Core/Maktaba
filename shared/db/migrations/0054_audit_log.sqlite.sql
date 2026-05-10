-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS audit_log (
    id            TEXT    PRIMARY KEY,
    occurred_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    category      TEXT    NOT NULL CHECK (category IN (
                            'auth', 'library', 'admin', 'data',
                            'config', 'keys', 'device', 'security',
                            'integrity', 'subscription')),
    action        TEXT    NOT NULL,
    actor_user    TEXT,
    actor_ip      TEXT,
    actor_source  TEXT,
    target_kind   TEXT,
    target_id     TEXT,
    payload       TEXT    NOT NULL DEFAULT '{}',
    error_id      TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS audit_log_occurred_at
    ON audit_log (occurred_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS audit_log_actor_user
    ON audit_log (actor_user, occurred_at DESC) WHERE actor_user IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS audit_log_category
    ON audit_log (category, occurred_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS audit_log_target
    ON audit_log (target_kind, target_id, occurred_at DESC) WHERE target_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS audit_log_target;
DROP INDEX IF EXISTS audit_log_category;
DROP INDEX IF EXISTS audit_log_actor_user;
DROP INDEX IF EXISTS audit_log_occurred_at;
DROP TABLE IF EXISTS audit_log;
-- +goose StatementEnd
