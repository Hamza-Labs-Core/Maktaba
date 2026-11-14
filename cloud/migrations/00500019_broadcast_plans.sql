-- +goose Up
-- Story 50.20: the Broadcaster tier and Pro's guest sharing. Expand only: new tables and new
-- columns with defaults, nothing the running release reads changes.

-- the home's broadcast plan (plans are per home, as 50.19's): NULL is Pro's guest sharing on a
-- Pro home and nothing elsewhere; 'broadcaster' is the add-on, which needs Pro. Extra channels
-- and extra 100-viewer blocks are the add-on's per-unit items. broadcast_test_publisher is a
-- service admin's grant: the home may broadcast while the broadcasting gate is shut (staging).
ALTER TABLE servers ADD COLUMN IF NOT EXISTS broadcast_plan TEXT
    CHECK (broadcast_plan IS NULL OR broadcast_plan IN ('broadcaster'));
ALTER TABLE servers ADD COLUMN IF NOT EXISTS broadcast_extra_channels INTEGER NOT NULL DEFAULT 0
    CHECK (broadcast_extra_channels BETWEEN 0 AND 100);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS broadcast_extra_viewers INTEGER NOT NULL DEFAULT 0
    CHECK (broadcast_extra_viewers BETWEEN 0 AND 100);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS broadcast_test_publisher BOOLEAN NOT NULL DEFAULT false;

-- the Broadcaster agreement each account accepted (§0): a new version is asked for at the next
-- publish. ip_hash is a salted hash, never the address.
CREATE TABLE IF NOT EXISTS broadcast_agreements (
    account_id  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    version     INTEGER     NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ip_hash     TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (account_id, version)
);

-- the channels a home published (§1's limits are counted here; 50.22 adds the ladder and the
-- ingest token). unpublished_at set: no longer counted.
CREATE TABLE IF NOT EXISTS broadcast_channels (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id      UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    slug           TEXT        NOT NULL,
    access         TEXT        NOT NULL CHECK (access IN ('invite','open')),
    on_demand      BOOLEAN     NOT NULL DEFAULT true,
    rungs          INTEGER     NOT NULL DEFAULT 1,
    top_kbps       INTEGER     NOT NULL DEFAULT 0,
    agreement_version INTEGER  NOT NULL,
    published_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    unpublished_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS broadcast_channels_live_slug ON broadcast_channels (server_id, slug)
    WHERE unpublished_at IS NULL;

-- the edge's 5-minute windows, one row per <publisher>:<window> (the idempotency key), kept so a
-- window posted twice counts once; the worker deletes them after 35 days
CREATE TABLE IF NOT EXISTS broadcast_usage_windows (
    publisher_id   UUID        NOT NULL,
    window_start   TIMESTAMPTZ NOT NULL,
    minutes_full   BIGINT      NOT NULL DEFAULT 0,
    minutes_lowest BIGINT      NOT NULL DEFAULT 0,
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (publisher_id, window_start)
);

-- a publisher's viewer-minutes this month, by how they were counted: within the allowance
-- (and past the pay-as-you-go cap, unbilled), in lowest, and bought pay-as-you-go
CREATE TABLE IF NOT EXISTS broadcast_usage_monthly (
    publisher_id   UUID        NOT NULL,
    month          DATE        NOT NULL,
    minutes_full   BIGINT      NOT NULL DEFAULT 0,
    minutes_lowest BIGINT      NOT NULL DEFAULT 0,
    minutes_payg   BIGINT      NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (publisher_id, month)
);

-- a publisher's pay-as-you-go viewer-hours: off unless the owner turns it on, with a monthly cap
CREATE TABLE IF NOT EXISTS broadcast_payg_settings (
    server_id  UUID        PRIMARY KEY REFERENCES servers(id) ON DELETE CASCADE,
    enabled    BOOLEAN     NOT NULL DEFAULT false,
    cap_cents  BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- one usage report per publisher and hour (id <publisher>:<month>:<hour>, the provider's
-- idempotency key), in whole viewer-hours, sent until the provider takes it
CREATE TABLE IF NOT EXISTS broadcast_usage_outbox (
    id         TEXT        PRIMARY KEY,
    server_id  UUID        NOT NULL,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    month      DATE        NOT NULL,
    hours      BIGINT      NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at    TIMESTAMPTZ,
    attempts   INTEGER     NOT NULL DEFAULT 0,
    last_error TEXT
);
CREATE INDEX IF NOT EXISTS broadcast_usage_outbox_unsent ON broadcast_usage_outbox (created_at) WHERE sent_at IS NULL;

-- the 80 % and 100 % notices of the allowance and of the cap, once a month each
CREATE TABLE IF NOT EXISTS broadcast_notices (
    server_id UUID        NOT NULL,
    month     DATE        NOT NULL,
    kind      TEXT        NOT NULL,
    sent_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, month, kind)
);

-- +goose Down
DROP TABLE IF EXISTS broadcast_notices;
DROP TABLE IF EXISTS broadcast_usage_outbox;
DROP TABLE IF EXISTS broadcast_payg_settings;
DROP TABLE IF EXISTS broadcast_usage_monthly;
DROP TABLE IF EXISTS broadcast_usage_windows;
DROP TABLE IF EXISTS broadcast_channels;
DROP TABLE IF EXISTS broadcast_agreements;
ALTER TABLE servers DROP COLUMN IF EXISTS broadcast_test_publisher;
ALTER TABLE servers DROP COLUMN IF EXISTS broadcast_extra_viewers;
ALTER TABLE servers DROP COLUMN IF EXISTS broadcast_extra_channels;
ALTER TABLE servers DROP COLUMN IF EXISTS broadcast_plan;
