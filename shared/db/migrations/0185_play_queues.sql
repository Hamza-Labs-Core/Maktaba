-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0185 (Epic 56, story 56.01) - a play queue on the server.
--
--   play_queues       one queue per Play or Shuffle: its source (a library with the grid's
--                     filters, a series, a season, a collection, a playlist or one video),
--                     whether it is shuffled and its seed (the order is md5(video id || seed)),
--                     how far the source was read (source_cursor, source_done), the current
--                     position and a version that every move bumps (a stale move is a 409)
--   play_queue_items  the materialized items in order: written in chunks of 500 as the current
--                     item nears the end; origin says whether the source, Play next or Add to
--                     queue put it there. Positions are never reused.
--
-- A queue is its owner's alone; the API's maintenance loop deletes queues not used for 7 days.
-- Not in a machine's copy (cluster_copy_excluded() is slot 0175's list and the two tables).
-- Additive.
--
CREATE TABLE IF NOT EXISTS play_queues (
    id             UUID        PRIMARY KEY,
    user_id        UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id      TEXT,
    source_kind    TEXT        NOT NULL
                   CHECK (source_kind IN ('library', 'series', 'season', 'collection', 'playlist', 'video')),
    source_id      TEXT        NOT NULL,
    source_filters JSONB       NOT NULL DEFAULT '{}'::jsonb,
    shuffle        BOOLEAN     NOT NULL DEFAULT false,
    seed           BIGINT,
    source_cursor  TEXT,
    source_done    BOOLEAN     NOT NULL DEFAULT false,
    current_pos    INTEGER     NOT NULL DEFAULT 0,
    version        INTEGER     NOT NULL DEFAULT 1,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    used_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS play_queues_user_used_idx ON play_queues (user_id, used_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS play_queue_items (
    queue_id UUID    NOT NULL REFERENCES play_queues(id) ON DELETE CASCADE,
    pos      INTEGER NOT NULL,
    video_id UUID    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    origin   TEXT    NOT NULL DEFAULT 'source' CHECK (origin IN ('source', 'next', 'added')),
    PRIMARY KEY (queue_id, pos)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0175's exclusion, with the play queues
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
        -- play queues (slot 0185): a user's own, moved on every Next; their routes run on
        -- the home (/api/me)
        'play_queues', 'play_queue_items'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
DROP TABLE IF EXISTS play_queue_items;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS play_queues;
-- +goose StatementEnd
