-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0194 - plugins: the plugin API's tables (Epic 54, stories 54.04-54.06, 54.08).
--
--   plugin_installs     one row per installed plugin (54.07): its version, digest, folder
--                       and validated manifest. The pipeline's plugin loader reads it; the
--                       API writes it (the lifecycle routes come with 54.07).
--   plugin_grants       what an admin granted a plugin (54.08): capabilities, libraries
--                       (none until an admin ticks one), and a generation that moves on
--                       every change. NOTIFY plugin_grants_changed tells the API's cache.
--   plugin_invocations  one row per running plugin job (54.04 §2): the sha256 of its
--                       token (never the token), the job, subject, libraries and grant
--                       generation it was minted with, its expiry and counters.
--   plugin_data         each plugin's own key-value data (54.04 §4), its own namespace.
--   plugin_writes       every change a plugin made to core data, with before and after,
--                       for "what it wrote", undo per plugin and purge (54.07 §7).
--   plugin_suggestions  changes a plugin suggests for an admin to accept (suggest.add).
--
-- job_stages.stage takes names up to 63 characters (a plugin stage is
-- p_<8 hex>_<name up to 40>, story 54.01 §4); chapters gain the source 'plugin'.
--
-- Only the home's API writes these (plugin stages run on the home only, README H12).
-- plugin_tables_guard() refuses any insert, update or delete by a member of maktaba_worker
-- (every joined machine's login role) that is not also a member of the table's owner,
-- as slot 0190 does for component_restarts. The grants stay the defaults of a new table:
-- the previous release re-makes maktaba_worker with them, and its worker-group check
-- reads a revoke as a change. A joined machine may SELECT them: none holds a secret
-- (plugin_invocations keeps only the sha256 of a token, never the token; grants are
-- capability names and library ids, which a joined machine reads elsewhere already).
--
-- A machine's copy leaves them out: cluster_copy_excluded() is slot 0191's list and the six.
--
-- Additive: new tables, a wider check, one more allowed chapter source.
--
-- the plugins pool (story 54.06 §2), before a worker syncs the graph; its size is the
-- graph's (a quarter of the home's cores), written by the sync
INSERT INTO worker_pools (pool, label, scope, size, default_size)
VALUES ('plugins', 'Plugins', 'global', 1, 1)
ON CONFLICT (pool) DO NOTHING;

ALTER TABLE job_stages DROP CONSTRAINT IF EXISTS job_stages_stage_check;
ALTER TABLE job_stages ADD CONSTRAINT job_stages_stage_check CHECK (stage ~ '^[a-z][a-z0-9_]{1,62}$');

ALTER TABLE chapters DROP CONSTRAINT IF EXISTS chapters_source_check;
ALTER TABLE chapters ADD CONSTRAINT chapters_source_check
    CHECK (source IN ('embedded', 'inferred', 'manual', 'plugin'));

CREATE TABLE IF NOT EXISTS plugin_installs (
    plugin_id        text        PRIMARY KEY,
    version          text        NOT NULL,
    digest           text        NOT NULL,
    path             text        NOT NULL,
    manifest         jsonb       NOT NULL,
    review_level     text        NOT NULL DEFAULT 'none'
                     CHECK (review_level IN ('none', 'developer', 'store', 'development')),
    installed_at     timestamptz NOT NULL DEFAULT now(),
    previous_version text,
    previous_digest  text,
    previous_path    text,
    previous_manifest jsonb
);

CREATE TABLE IF NOT EXISTS plugin_grants (
    plugin_id     text        PRIMARY KEY,
    capabilities  text[]      NOT NULL DEFAULT '{}',
    libraries     uuid[]      NOT NULL DEFAULT '{}',
    all_libraries boolean     NOT NULL DEFAULT false,
    generation    bigint      NOT NULL DEFAULT 1,
    granted_by    text        NOT NULL DEFAULT '',
    granted_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS plugin_invocations (
    id               text        PRIMARY KEY,
    token_hash       text        NOT NULL UNIQUE,
    plugin_id        text        NOT NULL,
    version          text        NOT NULL,
    job_id           bigint      NOT NULL,
    stage            text        NOT NULL,
    subject_kind     text        NOT NULL,
    subject_id       text        NOT NULL,
    libraries        uuid[]      NOT NULL DEFAULT '{}',
    all_libraries    boolean     NOT NULL DEFAULT false,
    grants           text[]      NOT NULL DEFAULT '{}',
    grant_generation bigint      NOT NULL,
    exp              timestamptz NOT NULL,
    calls            bigint      NOT NULL DEFAULT 0,
    bytes_written    bigint      NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    ended_at         timestamptz
);

CREATE TABLE IF NOT EXISTS plugin_data (
    plugin_id    text        NOT NULL,
    subject_kind text        NOT NULL,
    subject_id   text        NOT NULL,
    key          text        NOT NULL,
    value        jsonb       NOT NULL,
    exported     boolean     NOT NULL DEFAULT false,
    bytes        integer     NOT NULL,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plugin_id, subject_kind, subject_id, key)
);

CREATE TABLE IF NOT EXISTS plugin_writes (
    id          bigserial   PRIMARY KEY,
    plugin_id   text        NOT NULL,
    version     text        NOT NULL,
    job_id      bigint,
    target      text        NOT NULL,
    subject_id  uuid        NOT NULL,
    field       text        NOT NULL DEFAULT '',
    before      jsonb,
    after       jsonb,
    at          timestamptz NOT NULL DEFAULT now(),
    reverted_at timestamptz
);

CREATE TABLE IF NOT EXISTS plugin_suggestions (
    id           bigserial   PRIMARY KEY,
    plugin_id    text        NOT NULL,
    version      text        NOT NULL,
    job_id       bigint,
    subject_kind text        NOT NULL,
    subject_id   text        NOT NULL,
    field        text        NOT NULL,
    value        text        NOT NULL,
    reason       text        NOT NULL DEFAULT '',
    status       text        NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'declined')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    decided_at   timestamptz,
    decided_by   text
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS plugin_invocations_created_idx ON plugin_invocations (created_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS plugin_writes_plugin_idx ON plugin_writes (plugin_id, at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plugin_grants_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('plugin_grants_changed', NEW.plugin_id);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER plugin_grants_changed_trg
    AFTER INSERT OR UPDATE ON plugin_grants
    FOR EACH ROW EXECUTE FUNCTION plugin_grants_notify();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plugin_installs_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('plugin_installs_changed', COALESCE(NEW.plugin_id, OLD.plugin_id));
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER plugin_installs_changed_trg
    AFTER INSERT OR UPDATE OR DELETE ON plugin_installs
    FOR EACH ROW EXECUTE FUNCTION plugin_installs_notify();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION plugin_tables_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    owner name := (SELECT pg_get_userbyid(c.relowner) FROM pg_class c WHERE c.oid = TG_RELID);
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker')
       AND pg_has_role(current_user, 'maktaba_worker', 'MEMBER')
       AND NOT pg_has_role(current_user, owner, 'MEMBER')
    THEN
        RAISE EXCEPTION '%: only the home writes plugin tables (role %)', TG_TABLE_NAME, current_user
            USING ERRCODE = '42501';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['plugin_installs', 'plugin_grants', 'plugin_invocations', 'plugin_data',
                             'plugin_writes', 'plugin_suggestions'] LOOP
        EXECUTE format('CREATE OR REPLACE TRIGGER %I BEFORE INSERT OR UPDATE OR DELETE ON %I'
                       ' FOR EACH ROW EXECUTE FUNCTION plugin_tables_guard()', t || '_guard_trg', t);
    END LOOP;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- slot 0191's exclusion, with the plugin tables
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
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- dev only (README rule 6)
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
        'play_queues', 'play_queue_items'
    ]::text[]
$f$;
DROP INDEX IF EXISTS plugin_writes_plugin_idx;
DROP INDEX IF EXISTS plugin_invocations_created_idx;
DROP TRIGGER IF EXISTS plugin_installs_changed_trg ON plugin_installs;
DROP TRIGGER IF EXISTS plugin_grants_changed_trg ON plugin_grants;
DROP FUNCTION IF EXISTS plugin_installs_notify();
DROP FUNCTION IF EXISTS plugin_tables_guard() CASCADE;
DROP FUNCTION IF EXISTS plugin_grants_notify();
DROP TABLE IF EXISTS plugin_suggestions, plugin_writes, plugin_data, plugin_invocations,
    plugin_grants, plugin_installs;
DELETE FROM worker_pools WHERE pool = 'plugins';
ALTER TABLE chapters DROP CONSTRAINT IF EXISTS chapters_source_check;
ALTER TABLE chapters ADD CONSTRAINT chapters_source_check
    CHECK (source IN ('embedded', 'inferred', 'manual'));
ALTER TABLE job_stages DROP CONSTRAINT IF EXISTS job_stages_stage_check;
ALTER TABLE job_stages ADD CONSTRAINT job_stages_stage_check CHECK (stage ~ '^[a-z][a-z0-9_]{1,39}$');
-- +goose StatementEnd
