-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0054. Additive ALTERs on top of the
-- slot-0036 audit_log table. SQLite 3.35+ supports ADD COLUMN IF NOT
-- EXISTS and DROP COLUMN IF EXISTS. CHECK constraints can't be added
-- post-hoc on SQLite, so the category enum is enforced at the
-- application layer in dev/test environments.
--
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS occurred_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE audit_log
    ADD COLUMN IF NOT EXISTS actor_ip TEXT;
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
CREATE INDEX IF NOT EXISTS audit_log_occurred_at
    ON audit_log (occurred_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS audit_log_target
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
