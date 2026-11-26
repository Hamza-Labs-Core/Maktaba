-- store-accounts: optional idempotency key for outbox-delivered audit rows so retrying
-- delivery after a transient failure never inserts duplicate audit entries.
ALTER TABLE audit ADD COLUMN event_id TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS audit_event_id ON audit (event_id) WHERE event_id IS NOT NULL;
