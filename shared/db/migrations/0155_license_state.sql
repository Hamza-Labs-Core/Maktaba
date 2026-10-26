-- +goose Up
-- +goose StatementBegin
--
-- Slot 0155 (Epic 16 story 16.4, license validation): what the home last learned from
-- the license server, kept across a restart.
--
--   license_state (one row, id = 1):
--     revocations          the last signed revocation list (JSON, as the server sent it;
--                          the home verifies its signature again when it loads it)
--     revocations_at       when that list was fetched
--     clock_high_water     the latest time the home has seen (its own clock or the
--                          license server's): a clock set back cannot revive an expired
--                          license (story 16.4 EC "clock manipulation")
--     last_refresh_at      the last successful refresh with the license server
--     last_refresh_error   the last refresh's error, '' after a success
--     lock_reason          'revoked' when the license server revoked the applied
--                          license; '' otherwise
--
-- The table is the home's own: not in a machine's copy (cluster_copy_excluded()), like
-- `licenses`. The exclusion also names the tables slots 0156 (telemetry) and 0157
-- (feature flags) add, so those slots need not redefine it.
--
CREATE TABLE IF NOT EXISTS license_state (
    id                  SMALLINT    PRIMARY KEY CHECK (id = 1),
    revocations         TEXT        NOT NULL DEFAULT '',
    revocations_at      TIMESTAMPTZ,
    clock_high_water    TIMESTAMPTZ,
    last_refresh_at     TIMESTAMPTZ,
    last_refresh_error  TEXT        NOT NULL DEFAULT '',
    lock_reason         TEXT        NOT NULL DEFAULT ''
);
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0154's exclusion, with the license state, telemetry and feature flags
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

-- +goose Down
-- +goose StatementBegin
-- slot 0154's exclusion, as it was
CREATE OR REPLACE FUNCTION cluster_copy_excluded() RETURNS text[]
LANGUAGE sql IMMUTABLE AS $f$
    SELECT ARRAY[
        'home_secrets', 'tv_cluster_secret', 'tv_push_secrets', 'tv_simulcast_destinations',
        'refresh_tokens', 'password_reset_tokens', 'pairing_tickets', 'cluster_sso_codes',
        'idempotency_keys', 'licenses', 'devices', 'device_downloads', 'audit_log',
        'web_sessions',
        'cluster_nodes', 'cluster_node_stages', 'cluster_metrics_cache', 'cluster_log_cache',
        'cluster_cache_files', 'cluster_certs',
        'cluster_kept_aside',
        'goose_db_version', 'maktaba_schema_version',
        'segment_embeddings', 'semantic_store_state',
        'home_user_links', 'cloud_link_nonces', 'cloud_assertions_used', 'cloud_sign_ins',
        'tv_public_channels'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS license_state;
-- +goose StatementEnd
