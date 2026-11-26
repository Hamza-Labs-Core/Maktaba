-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_abuse_signals_created ON abuse_signals (created_at);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_log_user_email ON audit_log (lower(actor_email))
    WHERE actor_kind = 'user' AND actor_id IS NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_log_user_email;
DROP INDEX CONCURRENTLY IF EXISTS idx_abuse_signals_created;
