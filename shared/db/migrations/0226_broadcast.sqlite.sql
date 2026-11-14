-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0226 (Epic 50, story 50.22): home broadcasting's tables.
-- A SQLite home is a single machine: no roles and no machine copies. The broadcaster runs
-- only against Postgres (as the push relay does); the tables keep the schemas alike.
--
CREATE TABLE IF NOT EXISTS broadcast_channels (
    channel_id     TEXT     PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    slug           TEXT     NOT NULL UNIQUE,
    access         TEXT     NOT NULL DEFAULT 'invite' CHECK (access IN ('invite', 'open')),
    mode           TEXT     NOT NULL DEFAULT 'on_demand' CHECK (mode IN ('always_on', 'on_demand')),
    rungs          TEXT     NOT NULL DEFAULT '["src"]',
    countries      TEXT     NOT NULL DEFAULT '[]',
    state          TEXT     NOT NULL DEFAULT 'draft'
                            CHECK (state IN ('draft', 'live', 'offline', 'off_air', 'revoked')),
    cloud_id       TEXT,
    edge_channel   TEXT,
    dropped_rungs  INTEGER  NOT NULL DEFAULT 0,
    detail         TEXT     NOT NULL DEFAULT '',
    report         TEXT     NOT NULL DEFAULT '{}',
    reported_at    TEXT,
    published_at   TEXT,
    published_by   TEXT,
    updated_at     TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS broadcast_uplink (
    id           TEXT     PRIMARY KEY,
    measured_at  TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    kbps         INTEGER  NOT NULL CHECK (kbps > 0),
    source       TEXT     NOT NULL DEFAULT 'publish'
);

CREATE TABLE IF NOT EXISTS broadcast_confirmations (
    id                 TEXT     PRIMARY KEY,
    channel_id         TEXT     NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    agreement_version  INTEGER  NOT NULL,
    confirmed_by       TEXT     NOT NULL,
    confirmed_by_user  TEXT     REFERENCES users(id) ON DELETE SET NULL,
    confirmed_at       TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS broadcast_ingest_tokens (
    channel_id    TEXT     PRIMARY KEY REFERENCES broadcast_channels(channel_id) ON DELETE CASCADE,
    token         TEXT     NOT NULL,
    ingest_url    TEXT     NOT NULL,
    edge_channel  TEXT     NOT NULL,
    rungs         TEXT     NOT NULL DEFAULT '[]',
    max_top_kbps  INTEGER  NOT NULL DEFAULT 0,
    expires_at    TEXT     NOT NULL,
    fetched_at    TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS broadcast_ingest_tokens;
DROP TABLE IF EXISTS broadcast_confirmations;
DROP TABLE IF EXISTS broadcast_uplink;
DROP TABLE IF EXISTS broadcast_channels;
-- +goose StatementEnd
