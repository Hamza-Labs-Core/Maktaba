-- +goose Up
-- Accounts, billing and the service-admin console
-- (docs/accounts-billing-admin.md).
--
-- users.role:        'service_admin' is the cloud operator. It is granted
--                    only from the CLI; nothing about an email address or
--                    a home server's own roles confers it.
-- users.status:      'blocked' (the old admin action) becomes 'suspended'.
-- sessions:          rotation bookkeeping, so a rotated refresh token that
--                    comes back is recognised as a replay.
-- audit_log:         who did what; no FK so rows outlive the accounts they
--                    mention.
-- invoices:          the Stripe invoice ledger (paid / failed).
-- subscriptions:     the price and Stripe event time last applied, so an
--                    older event delivered late cannot undo a newer one.
-- stripe_events:     when the event was applied.
ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'user';
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('user','service_admin'));
UPDATE users SET status = 'suspended' WHERE status = 'blocked';
ALTER TABLE users ADD CONSTRAINT users_status_check CHECK (status IN ('active','suspended'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS suspended_reason TEXT;
CREATE INDEX IF NOT EXISTS idx_users_created ON users (created_at DESC);

ALTER TABLE sessions ADD COLUMN IF NOT EXISTS revoke_reason TEXT;
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS last_used_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS audit_log (
    id           BIGSERIAL   PRIMARY KEY,
    actor_id     UUID,
    actor_email  TEXT,
    action       TEXT        NOT NULL,
    target_kind  TEXT,
    target_id    TEXT,
    detail       JSONB       NOT NULL DEFAULT '{}'::jsonb,
    ip           TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_log_created ON audit_log (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_actor ON audit_log (actor_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_target ON audit_log (target_kind, target_id, created_at DESC);

CREATE TABLE IF NOT EXISTS invoices (
    stripe_invoice_id TEXT        PRIMARY KEY,
    user_id           UUID        REFERENCES users(id) ON DELETE CASCADE,
    status            TEXT        NOT NULL,
    amount_due_cents  BIGINT      NOT NULL DEFAULT 0,
    currency          TEXT        NOT NULL DEFAULT 'usd',
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_invoices_user ON invoices (user_id);

ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS stripe_price_id TEXT;
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS last_event_at TIMESTAMPTZ;
ALTER TABLE stripe_events ADD COLUMN IF NOT EXISTS applied_at TIMESTAMPTZ;

-- +goose Down
ALTER TABLE stripe_events DROP COLUMN IF EXISTS applied_at;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS last_event_at;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS stripe_price_id;
DROP TABLE IF EXISTS invoices;
DROP TABLE IF EXISTS audit_log;
ALTER TABLE sessions DROP COLUMN IF EXISTS last_used_at;
ALTER TABLE sessions DROP COLUMN IF EXISTS revoke_reason;
DROP INDEX IF EXISTS idx_users_created;
ALTER TABLE users DROP COLUMN IF EXISTS suspended_reason;
ALTER TABLE users DROP COLUMN IF EXISTS suspended_at;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_status_check;
UPDATE users SET status = 'blocked' WHERE status = 'suspended';
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users DROP COLUMN IF EXISTS role;
