-- +goose Up
-- +goose StatementBegin
--
-- Slot 0153 (Epic 34, story 34.1) - the public TV site: which channels the owner made
-- public, with the admin's rights confirmation (who, when, a note). A row exists for
-- every channel that was ever public: making a channel private clears only `public`.
-- The confirmation stays, for audit, and must be given again next time (design §2.1).
--
-- `slug` is the channel's address on the site (/watch/<slug>), unique, [a-z0-9-]. The
-- site never sees the channel id. A public channel's push is held always-on by the home
-- (story 34.3, through Epic 32's push and relay). The site keeps its own state in the
-- Worker, not here.
--
-- The table is not in a machine's copy (cluster_copy_excluded()): only the home's API
-- reads it.
--
-- maktaba_worker keeps the default rights (slot 0103) on this table, as on every
-- ordinary table: narrowing them here would be undone by an older release remaking
-- its worker group (compat-prev), as slot 0152 found. The rows hold no secret. So that a
-- machine cannot make a channel public by writing a row, `rights_sig` is an HMAC by the
-- home (its key in home_secrets, which machines cannot read) over the channel, the flag,
-- the slug and the confirmation: the home ignores a public row whose signature does not
-- match, nor one whose signature is not the latest the home recorded (a replay).
--
CREATE TABLE IF NOT EXISTS tv_public_channels (
    channel_id           UUID        PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    public               BOOLEAN     NOT NULL DEFAULT false,
    slug                 TEXT        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,39}$'),
    rights_confirmed_by  TEXT,
    rights_confirmed_at  TIMESTAMPTZ,
    rights_note          TEXT        NOT NULL DEFAULT '',
    rights_sig           TEXT,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by           TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0152's exclusion, with the public site's channels
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
        -- the public site's channels: read by the home's API only (a public decision is
        -- the home's; a machine's copy has no use for it)
        'tv_public_channels'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- slot 0152's exclusion, as it was
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
DROP TABLE IF EXISTS tv_public_channels;
-- +goose StatementEnd
