-- +goose Up
-- Story 50.26: viewer access. Expand only: new tables and new columns with defaults, nothing the
-- running release reads changes.

-- a channel's viewer settings: the publisher's per-channel cap (0: the publisher's limit only)
-- and whether search engines may index its page (default noindex)
ALTER TABLE broadcast_channels ADD COLUMN IF NOT EXISTS max_viewers INTEGER NOT NULL DEFAULT 0
    CHECK (max_viewers >= 0);
ALTER TABLE broadcast_channels ADD COLUMN IF NOT EXISTS indexable BOOLEAN NOT NULL DEFAULT false;

-- a publisher's viewer limits: open sessions per network (CGNAT: up to 50) and the daily
-- minutes of one guest pass or open session (0: none); access_version grows on every change the
-- edge must hear, access_pushed is the version the edge took
ALTER TABLE servers ADD COLUMN IF NOT EXISTS broadcast_max_sessions_per_ip INTEGER NOT NULL DEFAULT 3
    CHECK (broadcast_max_sessions_per_ip BETWEEN 1 AND 50);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS broadcast_daily_minutes INTEGER NOT NULL DEFAULT 0
    CHECK (broadcast_daily_minutes BETWEEN 0 AND 1440);
ALTER TABLE servers ADD COLUMN IF NOT EXISTS broadcast_access_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN IF NOT EXISTS broadcast_access_pushed BIGINT NOT NULL DEFAULT 0;

-- the invites a publisher created: the signed token is kept so the link can be shown again and
-- the short code mapped to it at the edge. channel_ids empty: every channel of the publisher.
-- created_by is the account (the cloud web app) or NULL (the home's Broadcast page, which
-- checks its own owner/admin role). The edge counts the uses; no viewer identity is kept here.
CREATE TABLE IF NOT EXISTS broadcast_invites (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id   UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    channel_ids UUID[]      NOT NULL DEFAULT '{}',
    label       TEXT        NOT NULL DEFAULT '' CHECK (char_length(label) <= 80),
    max_uses    INTEGER     NOT NULL CHECK (max_uses BETWEEN 1 AND 500),
    short_code  TEXT        NOT NULL,
    token       TEXT        NOT NULL,
    created_by  UUID        REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ,
    revoked_by  TEXT        NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS broadcast_invites_server ON broadcast_invites (server_id, created_at DESC);
-- a short code names one live invite of its publisher
CREATE UNIQUE INDEX IF NOT EXISTS broadcast_invites_code ON broadcast_invites (server_id, short_code)
    WHERE revoked_at IS NULL;

-- one device's guest pass the publisher revoked (the pass id is the edge's random id)
CREATE TABLE IF NOT EXISTS broadcast_revoked_passes (
    server_id  UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    pass_id    TEXT        NOT NULL CHECK (char_length(pass_id) <= 64),
    invite_id  UUID        REFERENCES broadcast_invites(id) ON DELETE CASCADE,
    revoked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, pass_id)
);

-- +goose Down
DROP TABLE IF EXISTS broadcast_revoked_passes;
DROP TABLE IF EXISTS broadcast_invites;
ALTER TABLE servers DROP COLUMN IF EXISTS broadcast_access_pushed;
ALTER TABLE servers DROP COLUMN IF EXISTS broadcast_access_version;
ALTER TABLE servers DROP COLUMN IF EXISTS broadcast_daily_minutes;
ALTER TABLE servers DROP COLUMN IF EXISTS broadcast_max_sessions_per_ip;
ALTER TABLE broadcast_channels DROP COLUMN IF EXISTS indexable;
ALTER TABLE broadcast_channels DROP COLUMN IF EXISTS max_viewers;
