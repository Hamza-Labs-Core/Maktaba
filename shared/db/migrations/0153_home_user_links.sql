-- +goose Up
-- +goose StatementBegin
--
-- Linked accounts (Epic 42 story 42.7): a home user links one Maktaba
-- Cloud account, and that account can then sign in to this home through
-- the cloud (a one-use assertion the cloud signs, verified here). The
-- home decides everything: the rights are the home user's, and the
-- link's scope caps them.
--
--   home_user_links      one cloud account per home user and one home user per cloud
--                        account; scope 'play' (default: watch only) or 'manage' (an
--                        admin's own link, set only on the home by that admin)
--   cloud_link_nonces    the link step's nonce: binds a cloud link assertion to the
--                        home user who started it (hashed, a few minutes)
--   cloud_assertions_used the assertions already used (one use each), kept until expiry
--   cloud_sign_ins       which web sessions and refresh families a cloud sign-in made (by
--                        the SHA-256 of their id, as web_session_keys: an id is a live
--                        credential), with the link's scope then; unlinking revokes them
--
-- A machine's copy holds home_user_links and cloud_sign_ins (a session a cloud sign-in
-- made keeps its scope there too, the home away); cluster_copy_excluded() gains the
-- nonces and the used assertions (each database's own). maktaba_worker: none.
CREATE TABLE IF NOT EXISTS home_user_links (
    user_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    cloud_user_id TEXT        NOT NULL UNIQUE,
    cloud_label   TEXT,
    scope         TEXT        NOT NULL DEFAULT 'play' CHECK (scope IN ('play', 'manage')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cloud_link_nonces (
    nonce_hash TEXT        PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cloud_assertions_used (
    jti        TEXT        PRIMARY KEY,
    expires_at TIMESTAMPTZ NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cloud_sign_ins (
    id_sha256  TEXT        PRIMARY KEY,
    kind       TEXT        NOT NULL CHECK (kind IN ('session', 'refresh')),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope      TEXT        NOT NULL CHECK (scope IN ('play', 'manage')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS cloud_sign_ins_user ON cloud_sign_ins (user_id);
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0152's exclusion, with the link step's nonces and the used assertions
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
        -- linked accounts (slot 0153): the link step's nonces and the assertions used
        'cloud_link_nonces', 'cloud_assertions_used'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0152's exclusion again
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

-- +goose StatementBegin
DROP TABLE IF EXISTS cloud_sign_ins;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cloud_assertions_used;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS cloud_link_nonces;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS home_user_links;
-- +goose StatementEnd
