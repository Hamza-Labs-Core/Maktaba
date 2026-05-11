-- +goose Up
-- Subscription/billing tables. `stripe_events` is the idempotency
-- ledger: every webhook id we've handled goes here so retries are
-- no-ops.
-- subscriptions: one row per user, even on the free tier. Carries
-- the Stripe customer id as its single source of truth (do not
-- denormalize onto `users`). `plan` is the tier (free/pro/family);
-- the billing interval lives in its own column so we don't flatten
-- a Cartesian product of tier × interval into a single string.
CREATE TABLE IF NOT EXISTS subscriptions (
    user_id              UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    plan                 TEXT        NOT NULL DEFAULT 'free'
                          CHECK (plan IN ('free','pro','family')),
    interval             TEXT
                          CHECK (interval IS NULL OR interval IN ('monthly','yearly')),
    stripe_customer_id   TEXT        UNIQUE,
    stripe_subscription_id TEXT      UNIQUE,
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

-- Family plan seats. The payer is the subscription owner; member
-- rows in this table inherit the payer's tier when resolved through
-- `subscriptions`. Spec: plan-25-12 §2 / plan-25-13 §1.
CREATE TABLE IF NOT EXISTS family_members (
    payer_user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    member_user_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invited_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted_at     TIMESTAMPTZ,
    PRIMARY KEY (payer_user_id, member_user_id)
);
CREATE INDEX IF NOT EXISTS family_members_member_idx ON family_members (member_user_id);

-- +goose Down
DROP TABLE IF EXISTS family_members;
DROP TABLE IF EXISTS stripe_events;
DROP TABLE IF EXISTS subscriptions;
