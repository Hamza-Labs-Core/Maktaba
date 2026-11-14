-- +goose Up
-- +goose StatementBegin
--
-- Slot 0226 (Epic 50, story 50.22) - home broadcasting: the channels the home publishes to
-- viewers outside it through Maktaba Cloud's broadcast edge.
--
--   broadcast_channels       one row per channel the admin set up for broadcasting: its
--                            address (slug), access (invite or open), mode (always_on or
--                            on_demand), ladder (rung names, top first), countries, and the
--                            state (draft, live, offline, off_air, revoked). The API writes
--                            the settings and the state; the home's front door (the
--                            broadcaster) writes `report` (what it uploads) and `detail`
--                            (why it paused: broadcast.tokenExpired, broadcast.clockSkew).
--   broadcast_uplink         each upload measurement (PUT /v1/ingest/speedtest), kbps
--   broadcast_confirmations  each rights confirmation: the channel, the Broadcaster
--                            agreement's version, the admin and the time (only the date
--                            and version go to the cloud)
--   broadcast_ingest_tokens  the channel's current ingest token (24 h, refreshed hourly by
--                            the API through the relay agent): a credential, so the
--                            machines' group role has no right on it
--
-- Every table is new (additive): an older release never reads them. A machine's copy
-- leaves all four out (cluster_copy_excluded() is slot 0222's list and these).
--
CREATE TABLE IF NOT EXISTS broadcast_channels (
    channel_id     uuid         PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    slug           text         NOT NULL UNIQUE
        CONSTRAINT broadcast_channels_slug_valid CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    access         text         NOT NULL DEFAULT 'invite'
        CONSTRAINT broadcast_channels_access_valid CHECK (access IN ('invite', 'open')),
    mode           text         NOT NULL DEFAULT 'on_demand'
        CONSTRAINT broadcast_channels_mode_valid CHECK (mode IN ('always_on', 'on_demand')),
    rungs          jsonb        NOT NULL DEFAULT '["src"]'::jsonb,
    countries      jsonb        NOT NULL DEFAULT '[]'::jsonb,
    state          text         NOT NULL DEFAULT 'draft'
        CONSTRAINT broadcast_channels_state_valid
        CHECK (state IN ('draft', 'live', 'offline', 'off_air', 'revoked')),
    cloud_id       text,
    edge_channel   text,
    dropped_rungs  integer      NOT NULL DEFAULT 0,
    detail         text         NOT NULL DEFAULT '',
    report         jsonb        NOT NULL DEFAULT '{}'::jsonb,
    reported_at    timestamptz,
    published_at   timestamptz,
    published_by   text,
    updated_at     timestamptz  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS broadcast_uplink (
    id           uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    measured_at  timestamptz  NOT NULL DEFAULT now(),
    kbps         integer      NOT NULL CONSTRAINT broadcast_uplink_kbps_positive CHECK (kbps > 0),
    source       text         NOT NULL DEFAULT 'publish'
);

CREATE TABLE IF NOT EXISTS broadcast_confirmations (
    id                 uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id         uuid         NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    agreement_version  integer      NOT NULL,
    confirmed_by       text         NOT NULL,
    confirmed_by_user  uuid         REFERENCES users(id) ON DELETE SET NULL,
    confirmed_at       timestamptz  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS broadcast_ingest_tokens (
    channel_id    uuid         PRIMARY KEY REFERENCES broadcast_channels(channel_id) ON DELETE CASCADE,
    token         text         NOT NULL,
    ingest_url    text         NOT NULL,
    edge_channel  text         NOT NULL,
    rungs         jsonb        NOT NULL DEFAULT '[]'::jsonb,
    max_top_kbps  integer      NOT NULL DEFAULT 0,
    expires_at    timestamptz  NOT NULL,
    fetched_at    timestamptz  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- slot 0103's default privileges gave the machines' group role rights on new
        -- tables: an ingest token is a credential, read only by the home's own login
        REVOKE ALL ON broadcast_ingest_tokens FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0222's exclusion, with home broadcasting's tables
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
        -- the license's state (slot 0155), the old telemetry sink (0156, empty since
        -- 0161) and feature flags (0157): the home's API alone reads them
        'license_state', 'telemetry_events', 'telemetry_web_vitals',
        'feature_flag_overrides', 'beta_cohorts',
        -- analytics (slot 0158): plays, viewings and the API's use per app, written by the
        -- home's API and front door every few seconds; a machine's copy has no use for them
        'tv_watch_sessions', 'api_usage_daily', 'api_usage_people_daily',
        -- pins (slot 0159): a user's own; the pins and browsing routes run on the home
        'user_library_pins',
        -- usage data waiting for Maktaba Cloud (slot 0161): the home's API alone sends it
        'telemetry_outbox',
        -- favorites and playlists (slot 0162): a user's own; their routes run on the home
        'user_favorites', 'user_playlists', 'user_playlist_items',
        -- the channel a library with no YouTube source looks like (slot 0175): the home's
        -- API alone writes and reads it
        'youtube_source_guesses',
        -- play queues (slot 0191): a user's own, moved on every Next; their routes run on
        -- the home (/api/me)
        'play_queues', 'play_queue_items',
        -- plugins (slot 0194): their installs, grants, job tokens, data, attributed writes
        -- and suggestions; plugin stages run on the home only and its API alone reads them
        'plugin_installs', 'plugin_grants', 'plugin_invocations', 'plugin_data',
        'plugin_writes', 'plugin_suggestions',
        -- plugins (slot 0195): an admin's keep-off and each plugin's health and breaker,
        -- the home's API and its pipeline alone read and write them
        'plugin_keep_off', 'plugin_health',
        -- plugins (slot 0197): each plugin's daily use of the home's models and the
        -- lookup slots its questions hold; the home's API alone writes them
        'plugin_compute_usage', 'compute_leases',
        -- plugins (slot 0199): the lineage a plugin write leaves on its subject, the home's
        -- API alone reads and writes it
        'plugin_write_lineage',
        -- plugins (slot 0203): the signed lists and the newest versions seen, the home's
        -- catalog loop alone writes them
        'plugin_trust', 'plugin_lists',
        -- plugins (slot 0204): the entitlements of paid plugins this home holds, signed by
        -- the store for this home and its store key; a machine's copy has no use for them
        'plugin_entitlements',
        -- parental controls (slot 0222): the parent PINs (a secret) and the wrong tries
        -- at them, a child's requests and a child's activity; their routes run on the
        -- home, whose API alone reads and writes them
        'parental_pins', 'parental_pin_failures', 'parental_requests', 'parental_events',
        -- home broadcasting (slot 0226): what the home publishes, its uplink measurements, the
        -- admins' rights confirmations and the ingest tokens; the home's API and front door
        -- alone read them (a token is a credential)
        'broadcast_channels', 'broadcast_uplink', 'broadcast_confirmations', 'broadcast_ingest_tokens'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0222's exclusion again
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
        -- the license's state (slot 0155), the old telemetry sink (0156, empty since
        -- 0161) and feature flags (0157): the home's API alone reads them
        'license_state', 'telemetry_events', 'telemetry_web_vitals',
        'feature_flag_overrides', 'beta_cohorts',
        -- analytics (slot 0158): plays, viewings and the API's use per app, written by the
        -- home's API and front door every few seconds; a machine's copy has no use for them
        'tv_watch_sessions', 'api_usage_daily', 'api_usage_people_daily',
        -- pins (slot 0159): a user's own; the pins and browsing routes run on the home
        'user_library_pins',
        -- usage data waiting for Maktaba Cloud (slot 0161): the home's API alone sends it
        'telemetry_outbox',
        -- favorites and playlists (slot 0162): a user's own; their routes run on the home
        'user_favorites', 'user_playlists', 'user_playlist_items',
        -- the channel a library with no YouTube source looks like (slot 0175): the home's
        -- API alone writes and reads it
        'youtube_source_guesses',
        -- play queues (slot 0191): a user's own, moved on every Next; their routes run on
        -- the home (/api/me)
        'play_queues', 'play_queue_items',
        -- plugins (slot 0194): their installs, grants, job tokens, data, attributed writes
        -- and suggestions; plugin stages run on the home only and its API alone reads them
        'plugin_installs', 'plugin_grants', 'plugin_invocations', 'plugin_data',
        'plugin_writes', 'plugin_suggestions',
        -- plugins (slot 0195): an admin's keep-off and each plugin's health and breaker,
        -- the home's API and its pipeline alone read and write them
        'plugin_keep_off', 'plugin_health',
        -- plugins (slot 0197): each plugin's daily use of the home's models and the
        -- lookup slots its questions hold; the home's API alone writes them
        'plugin_compute_usage', 'compute_leases',
        -- plugins (slot 0199): the lineage a plugin write leaves on its subject, the home's
        -- API alone reads and writes it
        'plugin_write_lineage',
        -- plugins (slot 0203): the signed lists and the newest versions seen, the home's
        -- catalog loop alone writes them
        'plugin_trust', 'plugin_lists',
        -- plugins (slot 0204): the entitlements of paid plugins this home holds, signed by
        -- the store for this home and its store key; a machine's copy has no use for them
        'plugin_entitlements',
        -- parental controls (slot 0222): the parent PINs (a secret) and the wrong tries
        -- at them, a child's requests and a child's activity; their routes run on the
        -- home, whose API alone reads and writes them
        'parental_pins', 'parental_pin_failures', 'parental_requests', 'parental_events'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS broadcast_ingest_tokens;
DROP TABLE IF EXISTS broadcast_confirmations;
DROP TABLE IF EXISTS broadcast_uplink;
DROP TABLE IF EXISTS broadcast_channels;
-- +goose StatementEnd
