-- +goose Up
-- +goose StatementBegin
--
-- Slot 0152 (Epic 43 stories 43.2 and 43.4): semantic search's vectors in Postgres.
--
--   segment_embeddings    one e5 vector per transcript segment (halfvec(384), HNSW,
--                         cosine), deleted with its segment or video. Only where the
--                         vector extension (pgvector) is installed: without it nothing
--                         is created and the semantic leg is off (full-text search).
--   semantic_store_state  which store search reads (chroma until the one-time copy of
--                         story 43.3 is done, postgres after) and the copy's progress.
--                         Always: the row says chroma here; the pipeline moves a home
--                         with no Chroma folder to postgres at start.
--   ensure_segment_embeddings()  creates the extension, the table and its indexes when
--                         pgvector is available (idempotent); this migration and every
--                         pipeline start call it, so a later start with pgvector
--                         installed creates what was deferred.
--   semantic_store_ready()  true when the extension and the table exist.
--
-- cluster_copy_excluded() gains both tables: a machine's copy never queries 13 GB of
-- vectors, and the store's state is each database's own (a new home re-indexes what it
-- lacks).
--
CREATE TABLE IF NOT EXISTS semantic_store_state (
    id            BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    store         TEXT NOT NULL DEFAULT 'chroma' CHECK (store IN ('chroma', 'postgres')),
    copy_cursor   TEXT,
    copied_rows   BIGINT NOT NULL DEFAULT 0,
    copy_done_at  TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO semantic_store_state (id) VALUES (TRUE) ON CONFLICT (id) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION semantic_store_ready() RETURNS boolean
LANGUAGE sql STABLE AS $f$
    SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')
       AND to_regclass('public.segment_embeddings') IS NOT NULL
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The table is made once the vector extension exists (created by the bundle's superuser,
-- Epic 43.1). SECURITY DEFINER: the table is the migrations' role's whoever calls this
-- (the pipeline at start), so slot 0103's default grants apply to it. The indexes come
-- with the table only: the one-time copy (43.3) may drop and rebuild the HNSW index
-- around its bulk load.
CREATE OR REPLACE FUNCTION ensure_segment_embeddings() RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF semantic_store_ready() THEN
        RETURN true;
    END IF;
    -- the home's workers start together: one creates, the others find it made
    PERFORM pg_advisory_xact_lock(hashtext('maktaba.segment_embeddings'));
    IF semantic_store_ready() THEN
        RETURN true;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
        -- the bundle's superuser creates it (pgbundle); the app role usually may not
        -- (pgvector is not a trusted extension): try, never fail the migration
        IF NOT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'vector') THEN
            RAISE NOTICE 'pgvector is not installed: semantic search stays off';
            RETURN false;
        END IF;
        BEGIN
            CREATE EXTENSION IF NOT EXISTS vector;
        EXCEPTION WHEN OTHERS THEN
            RAISE NOTICE 'pgvector could not be created: %', SQLERRM;
            RETURN false;
        END;
    END IF;
    IF to_regclass('public.segment_embeddings') IS NULL THEN
        CREATE TABLE IF NOT EXISTS segment_embeddings (
            segment_id  BIGINT PRIMARY KEY REFERENCES transcript_segments(id) ON DELETE CASCADE,
            video_id    UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
            library_id  UUID NOT NULL,
            language    TEXT,
            model       TEXT NOT NULL,
            embedding   halfvec(384) NOT NULL
        );
        -- a new, empty table, in a function (where CONCURRENTLY cannot run): cheap
        CREATE /* empty table */ INDEX IF NOT EXISTS segment_embeddings_hnsw
            ON segment_embeddings USING hnsw (embedding halfvec_cosine_ops)
            WITH (m = 16, ef_construction = 64);
        CREATE /* empty table */ INDEX IF NOT EXISTS segment_embeddings_video
            ON segment_embeddings (video_id);
        CREATE /* empty table */ INDEX IF NOT EXISTS segment_embeddings_library
            ON segment_embeddings (library_id);
    END IF;
    RETURN true;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
SELECT ensure_segment_embeddings();
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0150's exclusion, with the semantic store's tables
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
        'segment_embeddings', 'semantic_store_state'
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
        'goose_db_version', 'maktaba_schema_version'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS ensure_segment_embeddings();
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS semantic_store_ready();
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS segment_embeddings;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS semantic_store_state;
-- +goose StatementEnd
