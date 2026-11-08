-- +goose Up
-- Story 50.33 §8: who acted, by kind: a customer ('user'), a staff account ('staff'), or the
-- CLI on the leader ('cli'). Older rows were all customers' or the CLI's; they read 'user'.
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS actor_kind TEXT NOT NULL DEFAULT 'user'
    CHECK (actor_kind IN ('user','staff','cli'));
CREATE INDEX IF NOT EXISTS idx_audit_log_actor_kind ON audit_log (actor_kind, created_at DESC);

-- +goose Down
DROP INDEX IF EXISTS idx_audit_log_actor_kind;
ALTER TABLE audit_log DROP COLUMN IF EXISTS actor_kind;
