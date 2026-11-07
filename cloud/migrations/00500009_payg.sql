-- +goose Up
-- Story 50.19 §5, §6: pay-as-you-go with the account owner's monthly cap, Pro's extra streams,
-- and the hourly usage reports to the payment provider, queued so none is lost or sent twice.

-- the extra streams an account bought (subscription item 3), on each of its homes too so the
-- relay's cached home carries them (tier_changed refreshes it within a second)
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS extra_streams INTEGER NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS extra_streams INTEGER NOT NULL DEFAULT 0;

-- pay-as-you-go: off unless the owner turns it on, with a monthly cap in cents (no cap, no
-- pay-as-you-go); the months the 80 % and 100 % notices went out
CREATE TABLE IF NOT EXISTS payg_settings (
    user_id          UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled          BOOLEAN     NOT NULL DEFAULT false,
    cap_cents        BIGINT      NOT NULL DEFAULT 0,
    near_notified_on DATE,
    cap_notified_on  DATE,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- one usage report per home and hour (id <server_id>:<month>:<hour>, the provider's idempotency
-- key), in whole GB, sent until the provider takes it
CREATE TABLE IF NOT EXISTS billing_usage_outbox (
    id         TEXT        PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    server_id  UUID        NOT NULL,
    month      DATE        NOT NULL,
    gb         BIGINT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at    TIMESTAMPTZ,
    attempts   INTEGER     NOT NULL DEFAULT 0,
    last_error TEXT
);
CREATE INDEX IF NOT EXISTS billing_usage_outbox_unsent ON billing_usage_outbox (created_at) WHERE sent_at IS NULL;

-- a change of extra streams reaches the relay's cache like a plan change (50.05's trigger)
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION relay_notify_server() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('server_changed', OLD.slug);
        RETURN OLD;
    END IF;
    IF NEW.plan IS DISTINCT FROM OLD.plan OR NEW.extra_streams IS DISTINCT FROM OLD.extra_streams THEN
        PERFORM pg_notify('tier_changed', NEW.slug);
    END IF;
    IF NEW.slug IS DISTINCT FROM OLD.slug OR NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id
       OR NEW.server_secret_hash IS DISTINCT FROM OLD.server_secret_hash THEN
        PERFORM pg_notify('server_changed', OLD.slug);
        IF NEW.slug IS DISTINCT FROM OLD.slug THEN
            PERFORM pg_notify('server_changed', NEW.slug);
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION relay_notify_server() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM pg_notify('server_changed', OLD.slug);
        RETURN OLD;
    END IF;
    IF NEW.plan IS DISTINCT FROM OLD.plan THEN
        PERFORM pg_notify('tier_changed', NEW.slug);
    END IF;
    IF NEW.slug IS DISTINCT FROM OLD.slug OR NEW.owner_user_id IS DISTINCT FROM OLD.owner_user_id
       OR NEW.server_secret_hash IS DISTINCT FROM OLD.server_secret_hash THEN
        PERFORM pg_notify('server_changed', OLD.slug);
        IF NEW.slug IS DISTINCT FROM OLD.slug THEN
            PERFORM pg_notify('server_changed', NEW.slug);
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
DROP TABLE IF EXISTS billing_usage_outbox;
DROP TABLE IF EXISTS payg_settings;
ALTER TABLE servers DROP COLUMN IF EXISTS extra_streams;
ALTER TABLE subscriptions DROP COLUMN IF EXISTS extra_streams;
