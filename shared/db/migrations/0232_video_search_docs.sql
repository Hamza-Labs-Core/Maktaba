-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0232 (Epic 49 story 49.5): one keyword search document per video.
--
-- video_search_docs holds a video's searchable text in one row: its title, its speakers and
-- its body (the active transcript's lines in order), with the transcript it was built from
-- and when. Story 49.6's keyword_index stage builds and upserts it; story 49.7 ranks videos by
-- BM25 over it. The lines stay in transcript_segments: they give the passages and the jump
-- times for the videos found.
--
-- Two BM25 indexes (pg_textsearch), exact and stemmed, over the normalised fields (slot 0231):
--   video_search_docs_bm25       maktaba_normalize(title, speakers, body), maktaba_search
--   video_search_docs_bm25_stem  maktaba_stem_prepare(...), maktaba_stem
-- pg_textsearch is on PostgreSQL 18 only, and only where the server preloads it (the bundled
-- server's cluster SQL creates the extension: pgbundle). So the indexes are not made here:
-- maktaba_search_index_statements() returns the statements that make the ones missing (each
-- CREATE INDEX CONCURRENTLY IF NOT EXISTS, after dropping one a failed build left invalid),
-- nothing without pg_textsearch, and `maktaba-api migrate up` runs them after the migrations,
-- outside a transaction (api/internal/searchindex). Every start runs it: a home whose schema
-- migrated on PostgreSQL 16 gains the indexes at its first start on 18. Without pg_textsearch
-- (16, an operator's own server) the table has no BM25 index, and keyword search goes on by
-- its lines.
--
-- Not copied to the machines (cluster_copy_excluded); maktaba_worker keeps its default grants
-- (the pipeline's workers write it). The table is new and empty: no rewrite, no long lock.
--
CREATE TABLE IF NOT EXISTS video_search_docs (
    video_id      uuid PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    library_id    uuid NOT NULL,
    language      text,
    title         text NOT NULL DEFAULT '',
    speakers      text NOT NULL DEFAULT '',
    body          text NOT NULL,
    transcript_id uuid NOT NULL,
    built_at      timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS video_search_docs_library_idx ON video_search_docs (library_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION maktaba_search_index_statements() RETURNS SETOF text
LANGUAGE plpgsql STABLE SET search_path = public, pg_temp AS $f$
DECLARE
    i record;
    valid boolean;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_textsearch') THEN
        RETURN;
    END IF;
    FOR i IN SELECT * FROM (VALUES
        ('video_search_docs_bm25', 'public.maktaba_normalize', 'public.maktaba_search'),
        ('video_search_docs_bm25_stem', 'public.maktaba_stem_prepare', 'public.maktaba_stem')
    ) AS v(name, fn, cfg) LOOP
        SELECT x.indisvalid INTO valid FROM pg_index x
         WHERE x.indexrelid = to_regclass('public.' || i.name);
        CONTINUE WHEN valid;
        IF valid IS NOT NULL THEN -- a build that did not finish: made again
            RETURN NEXT format('DROP INDEX CONCURRENTLY IF EXISTS public.%I', i.name);
        END IF;
        RETURN NEXT format('CREATE INDEX CONCURRENTLY IF NOT EXISTS %I ON public.video_search_docs USING bm25 '
            '((ARRAY[%s(title), %2$s(speakers), %2$s(body)])) WITH (text_config = %L)', i.name, i.fn, i.cfg);
    END LOOP;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0230's exclusion, with video_search_docs
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
        'broadcast_uplink', 'broadcast_confirmations', 'broadcast_ingest_tokens',
        -- keyword search's per-video documents (slot 0232): built from the transcripts the
        -- copy already holds; a machine that serves search builds its own (story 49.6), a
        -- new home builds what it lacks
        'video_search_docs'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0230's exclusion
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

-- +goose StatementBegin
DROP FUNCTION IF EXISTS maktaba_search_index_statements();
DROP TABLE IF EXISTS video_search_docs;
-- +goose StatementEnd
