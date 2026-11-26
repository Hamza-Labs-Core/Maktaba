-- +goose Up
-- Resend webhook delivery events and address-hash suppressions (#1714):
--   mail_outbox.provider_id  Resend's message id saved when a mail is sent, so webhook events match
--                            the row without ever storing a recipient address in the clear.
--   mail_events              idempotent ledger of Resend webhook events (keyed by Svix event_id)
--                            for bounce/complaint counts and rate alerts; swept with mail_outbox.
--   mail_suppressions        keyed address hashes that hard-bounced or complained: future sign-up
--                            and notice mails are suppressed, while password resets are still tried.
ALTER TABLE mail_outbox
    ADD COLUMN IF NOT EXISTS provider_id TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_mail_outbox_provider
    ON mail_outbox (provider_id) WHERE provider_id <> '';

CREATE TABLE IF NOT EXISTS mail_events (
    event_id    TEXT        PRIMARY KEY,
    provider_id TEXT        NOT NULL DEFAULT '',
    outbox_id   UUID,
    to_hash     TEXT        NOT NULL DEFAULT '',
    event_type  TEXT        NOT NULL,
    bounce_type TEXT        NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mail_events_type_at
    ON mail_events (event_type, received_at);

CREATE INDEX IF NOT EXISTS idx_mail_events_provider
    ON mail_events (provider_id) WHERE provider_id <> '';

CREATE TABLE IF NOT EXISTS mail_suppressions (
    to_hash    TEXT        PRIMARY KEY,
    reason     TEXT        NOT NULL,
    event_id   TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE IF EXISTS mail_suppressions;
DROP TABLE IF EXISTS mail_events;
DROP INDEX IF EXISTS idx_mail_outbox_provider;
ALTER TABLE mail_outbox DROP COLUMN IF EXISTS provider_id;
