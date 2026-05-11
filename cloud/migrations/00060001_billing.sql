-- +goose Up
-- Subscription/billing tables. `stripe_events` is the idempotency
-- ledger: every webhook id we've handled goes here so retries are
-- no-ops.
CREATE TABLE IF NOT EXISTS subscriptions (
    user_id              UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    plan                 TEXT        NOT NULL DEFAULT 'free',
    stripe_customer_id   TEXT,
    stripe_subscription_id TEXT,
    status               TEXT        NOT NULL DEFAULT 'inactive',
    current_period_end   TIMESTAMPTZ,
    cancel_at_period_end BOOLEAN     NOT NULL DEFAULT FALSE,
    seats                INTEGER     NOT NULL DEFAULT 1,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS stripe_events (
    event_id   TEXT        PRIMARY KEY,
    type       TEXT        NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload    JSONB       NOT NULL
);

CREATE TABLE IF NOT EXISTS family_members (
    owner_user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_user_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invited_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at     TIMESTAMPTZ,
    PRIMARY KEY (owner_user_id, member_user_id)
);

-- +goose Down
DROP TABLE IF EXISTS family_members;
DROP TABLE IF EXISTS stripe_events;
DROP TABLE IF EXISTS subscriptions;
