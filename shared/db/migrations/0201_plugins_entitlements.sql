-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0201 - plugins: the entitlements of paid plugins (Epic 54, story 54.19 §6).
--
--   plugin_entitlements  each entitlement the store signed for this home (one per purchase),
--                        as it was checked offline when it was fetched (signed by an
--                        entitlement key of store-keys.json, for this home's store id and
--                        key): its plugin, product and validity, and the signed envelope. A
--                        paid plugin with no row valid now (with the 72-hour grace) waits,
--                        entitlement_missing; its queue and data are kept. The home's API
--                        refreshes the rows daily; nothing here names a payment provider.
--
-- As slot 0194's tables: the default grants, plugin_tables_guard() refuses writes by a
-- joined machine (maktaba_worker), and a machine's copy leaves it out
-- (cluster_copy_excluded() is slot 0199's list and this one). Additive.
--
CREATE TABLE IF NOT EXISTS plugin_entitlements (
    purchase_id  text        PRIMARY KEY,
    plugin_id    text        NOT NULL,
    product      text        NOT NULL,
    store_home   text        NOT NULL,
    valid_from   timestamptz NOT NULL,
    valid_until  timestamptz NOT NULL,
    issued_at    timestamptz NOT NULL,
    envelope     jsonb       NOT NULL,
    refreshed_at timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS plugin_entitlements_plugin_idx ON plugin_entitlements (plugin_id, valid_until);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER plugin_entitlements_guard_trg BEFORE INSERT OR UPDATE OR DELETE
    ON plugin_entitlements FOR EACH ROW EXECUTE FUNCTION plugin_tables_guard();
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0199's exclusion, with the entitlements
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
        -- plugins (slot 0201): the entitlements of paid plugins this home holds, signed by
        -- the store for this home and its store key; a machine's copy has no use for them
        'plugin_entitlements'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0199's exclusion again
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
        'plugin_write_lineage'
    ]::text[]
$f$;
DROP TABLE IF EXISTS plugin_entitlements;
-- +goose StatementEnd
