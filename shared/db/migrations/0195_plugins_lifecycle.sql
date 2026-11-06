-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0195 - plugins: keep-off and health (Epic 54, stories 54.07, 54.09).
--
--   plugin_keep_off  an admin's disable of a plugin (54.07 §1): while a row exists nothing
--                    but an admin's enable turns the plugin on (an update, a reinstall, a
--                    server update or a restore keeps it off). Keyed by plugin id and kept
--                    apart from plugin_installs, so an uninstall does not forget it.
--   plugin_health    each plugin's circuit breaker and counters (54.09 §2, §4): whether it
--                    tripped and why, its last 20 outcomes and its trap/timeout/memory and
--                    refusal times (the windows, rebuilt from here after a restart), jobs
--                    done and failed, and its last error. The home's pipeline writes it.
--
-- As slot 0194's tables: the default grants, plugin_tables_guard() refuses writes by a
-- joined machine (maktaba_worker), and a machine's copy leaves both out
-- (cluster_copy_excluded() is slot 0194's list and the two). Additive.
--
CREATE TABLE IF NOT EXISTS plugin_keep_off (
    plugin_id text        PRIMARY KEY,
    set_by    text        NOT NULL DEFAULT '',
    set_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plugin_health (
    plugin_id       text        PRIMARY KEY,
    tripped         boolean     NOT NULL DEFAULT false,
    tripped_at      timestamptz,
    trip_reason     text        NOT NULL DEFAULT '',
    recent          jsonb       NOT NULL DEFAULT '[]'::jsonb,
    crashes         jsonb       NOT NULL DEFAULT '[]'::jsonb,
    refusals        jsonb       NOT NULL DEFAULT '[]'::jsonb,
    jobs_done       bigint      NOT NULL DEFAULT 0,
    jobs_failed     bigint      NOT NULL DEFAULT 0,
    last_error_code text        NOT NULL DEFAULT '',
    last_error_at   timestamptz,
    updated_at      timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['plugin_keep_off', 'plugin_health'] LOOP
        EXECUTE format('CREATE OR REPLACE TRIGGER %I BEFORE INSERT OR UPDATE OR DELETE ON %I'
                       ' FOR EACH ROW EXECUTE FUNCTION plugin_tables_guard()', t || '_guard_trg', t);
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0194's exclusion, with the keep-off and health tables
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
        'plugin_keep_off', 'plugin_health'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0194's exclusion again
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
        'plugin_writes', 'plugin_suggestions'
    ]::text[]
$f$;
DROP TABLE IF EXISTS plugin_health, plugin_keep_off;
-- +goose StatementEnd
