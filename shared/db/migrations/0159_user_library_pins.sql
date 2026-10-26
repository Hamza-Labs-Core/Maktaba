-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0159 (Epic 45, story 45.4) - browsing the libraries. Each user keeps the libraries
-- they watch at hand: user_library_pins holds them in the order they chose (position 0
-- first). The API lets a user pin only a library they may see, at most 20.
--
-- The indexes serve the browsing rows: Recently Added across the visible libraries
-- (created_at, id), the same row in one library (library_id, created_at), and a library's
-- grid by publish date, which is the file's mtime (library_id, mtime).
--
-- Not in a machine's copy: cluster_copy_excluded() gains the table (slot 0158's list
-- plus this one; the pins and browsing routes run on the home, /api/me and
-- /api/libraries, and a previous release's copy check refuses a new copied table).
-- maktaba_worker: the default rights. Additive. NO TRANSACTION for the indexes on videos
-- (CONCURRENTLY: the table is large), as slot 0150.
--
CREATE TABLE IF NOT EXISTS user_library_pins (
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    position   INTEGER     NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, library_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_created_at_idx
    ON videos (created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_created_idx
    ON videos (library_id, created_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_library_mtime_idx
    ON videos (library_id, mtime DESC);
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0158's exclusion, with the pins
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
        'tv_watch_sessions', 'api_usage_daily', 'api_usage_people_daily',
        -- pins (slot 0159): a user's own; the pins and browsing routes run on the home
        'user_library_pins'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0158's exclusion again
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

-- +goose StatementBegin
DROP INDEX IF EXISTS videos_library_mtime_idx;
DROP INDEX IF EXISTS videos_library_created_idx;
DROP INDEX IF EXISTS videos_created_at_idx;
DROP TABLE IF EXISTS user_library_pins;
-- +goose StatementEnd
