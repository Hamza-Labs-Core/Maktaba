-- +goose Up
-- +goose StatementBegin
--
-- Slot 0175: a switch for every YouTube source, and the channel a library with none
-- looks like.
--
--   youtube_sources.last_on_mode  new_and_backfill | new_only: the source's last mode
--                                 other than off. NULL: it was never on (slot 0166
--                                 added every library's channel off). The API writes it
--                                 with every mode it sets that is not off; turning a
--                                 source back on (PATCH {"on": true}) restores it, or
--                                 new_and_backfill when it is NULL.
--   youtube_source_guesses        one row per library the API looked at for a channel
--                                 (POST /api/libraries/{id}/youtube/guess, an admin's
--                                 click, never a page load): the YouTube ids in its file
--                                 names (`#<id>`, `[<id>]`) and .info.json sidecars,
--                                 resolved through youtube_videos (then yt-dlp). kind,
--                                 external_id, url NULL: nothing found. Nothing is ever
--                                 added or turned on from it; the admin adds the source,
--                                 off.
--   job_stages youtube_tartube    (catalog, pool network_yt, sources lane 4, home, one
--                                 attempt): every library's sources from Tartube's
--                                 records (a channel or playlist whose download folder is
--                                 a library root's name), off, and what Tartube downloaded
--                                 seeded done. An admin's action (Admin > Libraries).
--
-- Backfill: every source on now keeps its mode. Additive: a nullable column, a new table
-- the home's API alone writes (no grant to the workers; left out of a machine's copy:
-- cluster_copy_excluded() is slot 0162's list and the guesses). A previous release
-- ignores both.
--
ALTER TABLE youtube_sources ADD COLUMN IF NOT EXISTS last_on_mode TEXT
    CHECK (last_on_mode IN ('new_and_backfill', 'new_only'));
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE youtube_sources SET last_on_mode = mode
 WHERE mode IN ('new_and_backfill', 'new_only') AND last_on_mode IS DISTINCT FROM mode;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_source_guesses (
    library_id         UUID        PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    kind               TEXT        CHECK (kind IN ('channel', 'playlist')),
    external_id        TEXT,
    url                TEXT,
    title              TEXT,
    -- where the ids came from: info_json (a sidecar named the channel) or filename
    evidence           TEXT        CHECK (evidence IN ('info_json', 'filename')),
    -- one id of the library's files that is the channel's (shown as the proof)
    sample_youtube_id  TEXT,
    -- YouTube ids found in the library's files, and how many of them are the channel's
    ids_found          INTEGER     NOT NULL DEFAULT 0,
    ids_matched        INTEGER     NOT NULL DEFAULT 0,
    looked_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK ((external_id IS NULL) = (kind IS NULL) AND (external_id IS NULL) = (url IS NULL))
);
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0162's exclusion, with the guesses
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
        'youtube_source_guesses'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- the Tartube import (Admin > Libraries): every library's sources from Tartube's records,
-- off, and what it downloaded seeded done; on the home (the pipeline's graph has it)
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('youtube_tartube', 'Tartube import', ARRAY['catalog']::text[], 'network_yt', 'sources', 4, 1, 0, 0, 1, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'home', true, NULL)
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'youtube_tartube';
-- +goose StatementEnd
-- +goose StatementBegin
-- slot 0162's exclusion again
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
        'user_favorites', 'user_playlists', 'user_playlist_items'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_source_guesses;
ALTER TABLE youtube_sources DROP COLUMN IF EXISTS last_on_mode;
-- +goose StatementEnd
