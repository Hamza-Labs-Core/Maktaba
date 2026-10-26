-- +goose NO TRANSACTION
-- +goose Up
--
-- Slot 0158 (Epic 44, story 44.1) - analytics counts every way of watching, and every
-- app's use of the API.
--
--   watch_sessions        gains stream_session_id (a play recorded server-side from the
--                         stream session every player opens: one row per stream session),
--                         client_app and client_version (from X-Maktaba-Client)
--   tv_watch_sessions     one row per viewing of a channel: in a Maktaba app or the web
--                         (surface 'app'), through Plex's Live TV and the HDHomeRun tuner
--                         ('tuner'; no person, the viewer's address hashed), or the remote
--                         TV ('remote', which keeps tv_remote_sessions; analytics reads that)
--   api_usage_daily       the API's requests per day, app and platform (errors, time, slow)
--   api_usage_people_daily who used which app on a day
--
-- The new tables are not in a machine's copy: cluster_copy_excluded() gains them (they
-- change every few seconds, and only the home's API reads them). maktaba_worker keeps
-- the default rights (slot 0103) on them, as on every ordinary table (slot 0152's
-- lesson). The rows hold no secret. NO TRANSACTION for the indexes (CONCURRENTLY), as
-- slot 0150.

ALTER TABLE watch_sessions ADD COLUMN IF NOT EXISTS stream_session_id UUID;
ALTER TABLE watch_sessions ADD COLUMN IF NOT EXISTS client_app TEXT;
ALTER TABLE watch_sessions ADD COLUMN IF NOT EXISTS client_version TEXT;

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS watch_sessions_stream_session_idx
    ON watch_sessions (stream_session_id) WHERE stream_session_id IS NOT NULL;

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS tv_watch_sessions (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id       UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id          UUID        REFERENCES users(id) ON DELETE CASCADE,
    surface          TEXT        NOT NULL CHECK (surface IN ('app', 'tuner', 'remote')),
    client_app       TEXT,
    client_version   TEXT,
    platform         TEXT,
    device_type      TEXT,
    client_addr_hash TEXT,
    started_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at         TIMESTAMPTZ,
    seconds          INTEGER     NOT NULL DEFAULT 0 CHECK (seconds >= 0),
    end_reason       TEXT        CHECK (end_reason IN ('closed', 'idle', 'replaced'))
);
-- +goose StatementEnd

CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_watch_sessions_started_idx
    ON tv_watch_sessions (started_at);

CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_watch_sessions_channel_idx
    ON tv_watch_sessions (channel_id, started_at);

CREATE INDEX CONCURRENTLY IF NOT EXISTS tv_watch_sessions_open_idx
    ON tv_watch_sessions (last_seen_at) WHERE ended_at IS NULL;

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_usage_daily (
    day        DATE    NOT NULL,
    client_app TEXT    NOT NULL,
    platform   TEXT    NOT NULL,
    requests   BIGINT  NOT NULL DEFAULT 0 CHECK (requests >= 0),
    errors     BIGINT  NOT NULL DEFAULT 0 CHECK (errors >= 0),
    total_ms   BIGINT  NOT NULL DEFAULT 0 CHECK (total_ms >= 0),
    slow       BIGINT  NOT NULL DEFAULT 0 CHECK (slow >= 0),
    PRIMARY KEY (day, client_app, platform)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS api_usage_people_daily (
    day            DATE NOT NULL,
    client_app     TEXT NOT NULL,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_version TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (day, client_app, user_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0155's exclusion, with the analytics tables
CREATE OR REPLACE FUNCTION cluster_copy_excluded() RETURNS text[]
LANGUAGE sql IMMUTABLE AS $f$
    SELECT ARRAY[
        -- secrets
        'home_secrets', 'tv_cluster_secret', 'tv_push_secrets', 'tv_simulcast_destinations',
        'refresh_tokens', 'password_reset_tokens', 'pairing_tickets', 'cluster_sso_codes',
        'idempotency_keys', 'licenses', 'devices', 'device_downloads', 'audit_log',
        -- live cookies (their hashes are copied: web_session_keys)
        'web_sessions',
        -- the machines' own (a machine's row of cluster_nodes is published to it alone)
        'cluster_nodes', 'cluster_node_stages', 'cluster_metrics_cache', 'cluster_log_cache',
        'cluster_cache_files', 'cluster_certs',
        -- what an old home wrote after an emergency took the home from it (the home's to
        -- apply or not)
        'cluster_kept_aside',
        -- each database's own
        'goose_db_version', 'maktaba_schema_version',
        -- semantic search's vectors (never queried on a machine; a new home indexes
        -- again what it lacks) and which store search reads
        'segment_embeddings', 'semantic_store_state',
        -- linked accounts (slot 0153): the home's own
        'home_user_links', 'cloud_link_nonces', 'cloud_assertions_used', 'cloud_sign_ins',
        -- the public site's channels (slot 0154): read by the home's API only (a public
        -- decision is the home's; a machine's copy has no use for it)
        'tv_public_channels',
        -- the license's state (slot 0155), telemetry (0156) and feature flags (0157):
        -- the home's API alone reads them
        'license_state', 'telemetry_events', 'telemetry_web_vitals',
        'feature_flag_overrides', 'beta_cohorts',
        -- analytics (slot 0158): plays, viewings and the API's use per app, written by the
        -- home's API and front door every few seconds; a machine's copy has no use for them
        'tv_watch_sessions', 'api_usage_daily', 'api_usage_people_daily'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0155's exclusion again
CREATE OR REPLACE FUNCTION cluster_copy_excluded() RETURNS text[]
LANGUAGE sql IMMUTABLE AS $f$
    SELECT ARRAY[
        -- secrets
        'home_secrets', 'tv_cluster_secret', 'tv_push_secrets', 'tv_simulcast_destinations',
        'refresh_tokens', 'password_reset_tokens', 'pairing_tickets', 'cluster_sso_codes',
        'idempotency_keys', 'licenses', 'devices', 'device_downloads', 'audit_log',
        -- live cookies (their hashes are copied: web_session_keys)
        'web_sessions',
        -- the machines' own (a machine's row of cluster_nodes is published to it alone)
        'cluster_nodes', 'cluster_node_stages', 'cluster_metrics_cache', 'cluster_log_cache',
        'cluster_cache_files', 'cluster_certs',
        -- what an old home wrote after an emergency took the home from it (the home's to
        -- apply or not)
        'cluster_kept_aside',
        -- each database's own
        'goose_db_version', 'maktaba_schema_version',
        -- semantic search's vectors (never queried on a machine; a new home indexes
        -- again what it lacks) and which store search reads
        'segment_embeddings', 'semantic_store_state',
        -- linked accounts (slot 0153): the home's own
        'home_user_links', 'cloud_link_nonces', 'cloud_assertions_used', 'cloud_sign_ins',
        -- the public site's channels (slot 0154): read by the home's API only (a public
        -- decision is the home's; a machine's copy has no use for it)
        'tv_public_channels',
        -- the license's state (slot 0155), telemetry (0156) and feature flags (0157):
        -- the home's API alone reads them
        'license_state', 'telemetry_events', 'telemetry_web_vitals',
        'feature_flag_overrides', 'beta_cohorts'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS api_usage_people_daily;
DROP TABLE IF EXISTS api_usage_daily;
DROP INDEX IF EXISTS tv_watch_sessions_open_idx;
DROP INDEX IF EXISTS tv_watch_sessions_channel_idx;
DROP INDEX IF EXISTS tv_watch_sessions_started_idx;
DROP TABLE IF EXISTS tv_watch_sessions;
DROP INDEX IF EXISTS watch_sessions_stream_session_idx;
ALTER TABLE watch_sessions DROP COLUMN IF EXISTS client_version;
ALTER TABLE watch_sessions DROP COLUMN IF EXISTS client_app;
ALTER TABLE watch_sessions DROP COLUMN IF EXISTS stream_session_id;
-- +goose StatementEnd
