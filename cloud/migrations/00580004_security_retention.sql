-- +goose Up
-- Build the indexes in the migration transaction. Concurrent index builds
-- would wait on the migrator's separate advisory-lock transaction.
CREATE INDEX IF NOT EXISTS idx_abuse_signals_created ON abuse_signals (created_at);
CREATE INDEX IF NOT EXISTS idx_audit_log_user_email ON audit_log (lower(actor_email))
    WHERE actor_kind = 'user' AND actor_id IS NULL;

-- +goose Down
DROP INDEX IF EXISTS idx_audit_log_user_email;
DROP INDEX IF EXISTS idx_abuse_signals_created;
