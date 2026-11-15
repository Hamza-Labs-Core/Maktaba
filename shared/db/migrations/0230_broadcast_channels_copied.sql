-- +goose Up
-- +goose StatementBegin
--
-- Slot 0230 (Epic 50, story 50.23) - a machine's copy holds broadcast_channels.
--
-- channel_publishable_only(channel) (slot 0228) is the one rule that limits a published
-- channel to publishable items: the scheduler (pipeline/channels/repo.py) and the channel
-- player (the livetv plugins' statements) call it. It reads broadcast_channels, which slot
-- 0227 left out of a machine's copy, so on a machine running on its copy while the home is
-- away it answered false for every channel and a published channel was built from its full
-- rules. Every other input of the rule already travels (app_settings, publishable_sources,
-- broadcast_takedowns, the item marks), so broadcast_channels now does too: it holds a
-- channel's broadcast settings and state (a slug, access, mode, rungs, countries, the
-- edge's opaque channel id, the uplink's report), no credential. The ingest tokens stay
-- out, as do the uplink measurements and the rights confirmations.
--
-- cluster_copy_excluded drops: broadcast_channels
--
-- A machine's publication gains the table the next time its copy is brought up to date
-- (cluster_copy_publication, slot 0147). Additive: the function is re-created with one
-- name fewer; Down restores slot 0227's list.
--
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
        -- home broadcasting (slot 0227): its uplink measurements, the admins' rights
        -- confirmations and the ingest tokens; the home's API and front door alone read them
        -- (a token is a credential). broadcast_channels is copied from slot 0230.
        'broadcast_uplink', 'broadcast_confirmations', 'broadcast_ingest_tokens'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0227's exclusion again
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
        -- home broadcasting (slot 0227): what the home publishes, its uplink measurements, the
        -- admins' rights confirmations and the ingest tokens; the home's API and front door
        -- alone read them (a token is a credential)
        'broadcast_channels', 'broadcast_uplink', 'broadcast_confirmations', 'broadcast_ingest_tokens'
    ]::text[]
$f$;
-- +goose StatementEnd
