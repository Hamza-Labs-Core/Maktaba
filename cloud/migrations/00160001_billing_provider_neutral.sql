-- +goose Up
-- Billing goes provider-neutral (owner's rule of 2026-11-03: the
-- payment provider is not chosen yet, and the cloud names none). The
-- columns and the event ledger that 0006 and 0012 named after one
-- provider are renamed, and each billing table gets a `provider`
-- column: the [billing].provider id that wrote the row, or "manual"
-- for a plan a service admin set by hand.
--
-- Renames only; no row is rewritten. Rows that predate this migration
-- (none in production: the cloud was never deployed) get provider
-- 'legacy' from a column default that is dropped again at once.

-- subscriptions
ALTER TABLE subscriptions RENAME COLUMN stripe_customer_id TO provider_customer_id;
ALTER TABLE subscriptions RENAME COLUMN stripe_subscription_id TO provider_subscription_id;
ALTER TABLE subscriptions RENAME COLUMN stripe_price_id TO provider_price_id;
ALTER TABLE subscriptions RENAME CONSTRAINT subscriptions_stripe_customer_id_key TO subscriptions_provider_customer_id_key;
ALTER TABLE subscriptions RENAME CONSTRAINT subscriptions_stripe_subscription_id_key TO subscriptions_provider_subscription_id_key;
-- NULL: no provider has touched the row (a free account).
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS provider TEXT;

-- invoices
ALTER TABLE invoices RENAME COLUMN stripe_invoice_id TO provider_invoice_id;
ALTER TABLE invoices ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE invoices ALTER COLUMN provider DROP DEFAULT;

-- the webhook idempotency ledger: one row per (provider, event id)
ALTER TABLE stripe_events RENAME TO provider_events;
ALTER TABLE provider_events RENAME COLUMN event_id TO provider_event_id;
ALTER TABLE provider_events ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE provider_events ALTER COLUMN provider DROP DEFAULT;
ALTER TABLE provider_events DROP CONSTRAINT stripe_events_pkey;
ALTER TABLE provider_events ADD CONSTRAINT provider_events_pkey PRIMARY KEY (provider, provider_event_id);

-- +goose Down
ALTER TABLE provider_events DROP CONSTRAINT provider_events_pkey;
ALTER TABLE provider_events ADD CONSTRAINT stripe_events_pkey PRIMARY KEY (provider_event_id);
ALTER TABLE provider_events DROP COLUMN IF EXISTS provider;
ALTER TABLE provider_events RENAME COLUMN provider_event_id TO event_id;
ALTER TABLE provider_events RENAME TO stripe_events;

ALTER TABLE invoices DROP COLUMN IF EXISTS provider;
ALTER TABLE invoices RENAME COLUMN provider_invoice_id TO stripe_invoice_id;

ALTER TABLE subscriptions DROP COLUMN IF EXISTS provider;
ALTER TABLE subscriptions RENAME CONSTRAINT subscriptions_provider_subscription_id_key TO subscriptions_stripe_subscription_id_key;
ALTER TABLE subscriptions RENAME CONSTRAINT subscriptions_provider_customer_id_key TO subscriptions_stripe_customer_id_key;
ALTER TABLE subscriptions RENAME COLUMN provider_price_id TO stripe_price_id;
ALTER TABLE subscriptions RENAME COLUMN provider_subscription_id TO stripe_subscription_id;
ALTER TABLE subscriptions RENAME COLUMN provider_customer_id TO stripe_customer_id;
