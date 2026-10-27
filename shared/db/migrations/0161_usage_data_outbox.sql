-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0161 (Epic 16, stories 16.5 and 16.7 reworked): anonymous usage data goes to
-- Maktaba Cloud, for improving Maktaba, no longer to a sink on the home. The home's admin
-- already sees everything on the home, by name (Epic 44's analytics), so what the old
-- sink (slot 0156: telemetry_events, telemetry_web_vitals) held is deleted. Its tables
-- stay, empty, for the previous release (an automatic update's rollback runs it on this
-- database, and it still writes there); a later slot drops them.
--
--   telemetry_outbox   what waits to go to Maktaba Cloud: the home's own daily report
--                      (kind 'server', only while the admin's switch is on), an app's
--                      batch its user agreed to share (kind 'app'; the home is a pipe and
--                      forwards it whatever the server switch says), or a request to
--                      delete what an app install sent (kind 'forget').
--     install_id       the random id of the home's install or of the app's (never a user)
--     body             the batch as it goes to the cloud (already scrubbed by the API)
--     attempts, next_attempt_at, last_error
--                      the sender's retries (backoff); a row older than seven days is
--                      dropped unsent (a home without internet keeps nothing for long)
--
-- Not in a machine's copy: cluster_copy_excluded() is slot 0159's list and the outbox. maktaba_worker: the default rights (no narrowing, as 0152).
-- NO TRANSACTION for the index (CONCURRENTLY), as slot 0150.
--
DELETE FROM telemetry_web_vitals;
DELETE FROM telemetry_events;

CREATE TABLE IF NOT EXISTS telemetry_outbox (
    id              BIGSERIAL   PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    kind            TEXT        NOT NULL CHECK (kind IN ('server', 'app', 'forget')),
    install_id      TEXT        NOT NULL,
    body            JSONB       NOT NULL,
    attempts        INTEGER     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT        NOT NULL DEFAULT ''
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS telemetry_outbox_next_idx
    ON telemetry_outbox (next_attempt_at);
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0159's exclusion and the outbox
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
        'telemetry_outbox'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0159's exclusion again
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

-- +goose StatementBegin
-- what the old sink held is not brought back
DROP TABLE IF EXISTS telemetry_outbox;
-- +goose StatementEnd
