-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0235 (Epic 49 story 49.8): keyword search's terms dictionary, for suggestions as
-- you type and did-you-mean.
--
-- search_terms has one row per library and term:
--   term   the word's key, as search matches it: maktaba_normalize, lower case, Latin
--          accents removed (api/internal/searchterms and the pipeline's
--          search/terms.py, held equal by a test)
--   shown  a spelling the library's transcripts use, harakat and tatweel removed (a
--          suggestion shows مدرسة, not the key مدرسه)
--   df     how many of the library's documents hold the word
-- The keyword_index stage keeps it. When it writes a video's document, it takes the old
-- document's words off the counts (only if they were counted: video_search_docs.terms_counted)
-- and adds the new document's words, in the same transaction. The stage's version moves to
-- 2, so its plan rebuilds every document once and fills the dictionary, one job at a time.
-- Counts are per library so a suggestion never shows a word from a library the caller cannot
-- read. A trigram index on the key spelled in ASCII (trgm) serves did-you-mean, a
-- text_pattern_ops btree the prefix lookups of suggestions, and one on df the commonest
-- prefixes (ال starts most Arabic words: the most frequent come first, and the lookup stops).
--
-- search_speakers is the same for the named speakers, per library: one row per name and
-- each of its word starts (الشيخ أحمد is found by الشي and by أحم), with how many of the
-- library's documents name the speaker. Suggestions read it rather than every video's
-- speakers.
--
-- videos_shown_title_prefix serves the title suggestions: the title a video shows, lower
-- case, by prefix, the first three in order (an index scan that stops).
--
-- Not copied to the machines (cluster_copy_excluded), as video_search_docs is not. Both
-- tables are new or gain a column with a constant default: no rewrite.
--
CREATE TABLE IF NOT EXISTS search_terms (
    library_id uuid    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    term       text    NOT NULL,
    shown      text    NOT NULL,
    df         integer NOT NULL,
    -- the key with its Arabic letters spelled in ASCII, for pg_trgm: the bundled server's
    -- database is --locale=C, where pg_trgm finds no trigram in an Arabic word (its letters
    -- are not alphanumeric in C). The 29 letters a key can hold (after the folding) map to
    -- a-z and 0-2; did-you-mean compares a key only with keys of the same script.
    trgm       text GENERATED ALWAYS AS (translate(term,
                   'ءابتثجحخدذرزسشصضطظعغفقكلمنهوي', 'abcdefghijklmnopqrstuvwxyz012')) STORED,
    PRIMARY KEY (library_id, term)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_terms_trgm ON search_terms USING gin (trgm gin_trgm_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_terms_term_prefix ON search_terms (term text_pattern_ops);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_terms_df ON search_terms (df DESC, term);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS search_speakers (
    library_id uuid    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    key        text    NOT NULL,
    name       text    NOT NULL,
    df         integer NOT NULL,
    PRIMARY KEY (library_id, key, name)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS search_speakers_key_prefix ON search_speakers (key text_pattern_ops);
-- +goose StatementEnd

-- +goose StatementBegin
-- a restricted account's speaker suggestions check each name against the videos it may see
CREATE INDEX CONCURRENTLY IF NOT EXISTS speakers_name_idx ON speakers (name);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS videos_shown_title_prefix ON videos
    (lower(COALESCE(NULLIF(display_title_manual, ''), NULLIF(display_title, ''), title)) text_pattern_ops)
    WHERE deleted_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE video_search_docs ADD COLUMN IF NOT EXISTS terms_counted boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0232's exclusion, and search_terms
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
        'video_search_docs',
        -- keyword search's terms and speakers dictionaries (slot 0235): built by the
        -- keyword_index stage from the documents, which are not copied either
        'search_terms', 'search_speakers'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- the keyword_index stage is at version 2 (its documents' words counted: the graph's
-- KEYWORD_INDEX_VERSION), as a freshly migrated database's seed must match the graph. The
-- seed of slot 0233 again, at version 2 (a stage the graph sync already moved on keeps its
-- own): no row's data is lost on the way up, and the Down puts version 1 back, so a
-- rollback runs the Down scripts and keeps the database (updater's Reversible)
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement, enabled, disabled_reason)
VALUES
    ('keyword_index', 'Keyword index', ARRAY['video', 'library']::text[], 'cpu', 'speech', 5, 2, 0, 0, 3, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'any', true, NULL)
ON CONFLICT (stage) DO UPDATE SET version = 2 WHERE job_stages.version < 2;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 1 WHERE stage = 'keyword_index' AND version = 2;
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0232's exclusion
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

-- +goose StatementBegin
ALTER TABLE video_search_docs DROP COLUMN IF EXISTS terms_counted;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS videos_shown_title_prefix;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS speakers_name_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS search_speakers;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS search_terms;
-- +goose StatementEnd
