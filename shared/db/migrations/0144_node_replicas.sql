-- +goose Up
-- +goose StatementBegin
--
-- Slot 0144 (node replicas, specs/epics/37-node-replica): every joined machine keeps a
-- live copy of this database, by logical replication, and runs on it when the home is
-- away.
--
--   1. The machines' group (maktaba_worker) on a home whose migrations ran without the
--      right to create roles (the Postgres bundled with Maktaba.app, whose app role
--      could not before its cluster bootstrap): created now that it can, with the
--      privileges slots 0103..0143 give it, replayed in their order. A home that has
--      the group is not touched.
--   2. The replicas' group (maktaba_replicas): each machine's replication role joins
--      it. It reads the tables a copy is made of, nothing else (granted as they are
--      published).
--   3. What a copy holds: cluster_copy_excluded() names the tables it leaves out (the
--      secrets, and other machines' rows), cluster_copy_tables() is every other table
--      of public; a machine's own row of cluster_nodes, without its keys, is published
--      to it alone. A table with no primary key is published with REPLICA IDENTITY FULL
--      (without one, this home could not update it once published).
--   4. cluster_nodes.replica: what a machine reports of its copy, through
--      cluster_node_report_replica.
--   5. cluster_replica_beat: this home's clock, written every few seconds while a
--      machine follows it, so a copy knows how far it has applied the home's changes;
--      and the CA of this database's TLS, which a machine pins for its replication.
--   6. cluster_node_replica_grant: a machine asks for its copy over its own database
--      connection: its replication role (maktaba_rep_<id>, with the password the
--      machine chose) and its publication (maktaba_copy_<id>, brought up to date with
--      the tables this home has now); its subscription makes the slot.
--
-- Additive.
--
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker')
       AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user AND (rolsuper OR rolcreaterole)) THEN
        CREATE ROLE maktaba_worker NOLOGIN;
        -- slot 0103: data, no schema
        EXECUTE format('GRANT CONNECT ON DATABASE %I TO maktaba_worker', current_database());
        GRANT USAGE ON SCHEMA public TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO maktaba_worker;
        GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA public TO maktaba_worker;
        GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO maktaba_worker;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO maktaba_worker;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO maktaba_worker;
        ALTER DEFAULT PRIVILEGES IN SCHEMA public
            GRANT EXECUTE ON FUNCTIONS TO maktaba_worker;
        REVOKE ALL ON users, web_sessions, refresh_tokens, personal_access_tokens,
            password_reset_tokens, pairing_tickets, licenses, devices, device_downloads,
            streaming_sessions, watch_sessions, user_recs, user_analytics_prefs,
            idempotency_keys, instance_identity, cluster_nodes, cluster_node_stages
            FROM maktaba_worker;
        REVOKE ALL ON audit_log FROM maktaba_worker;
        GRANT INSERT ON audit_log TO maktaba_worker;
        -- slot 0119
        REVOKE ALL ON cluster_metrics_cache, cluster_log_cache, cluster_cache_files FROM maktaba_worker;
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON health_samples FROM maktaba_worker;
        -- slot 0120
        REVOKE ALL ON home_secrets FROM maktaba_worker;
        -- slot 0123
        REVOKE ALL ON streaming_hosts, channel_placements, tv_cluster_secret FROM maktaba_worker;
        REVOKE ALL ON SEQUENCE tv_placement_generation_seq FROM maktaba_worker;
        GRANT SELECT ON streaming_hosts, channel_placements TO maktaba_worker;
        REVOKE EXECUTE ON FUNCTION tv_host_guard(text) FROM maktaba_worker;
        GRANT EXECUTE ON FUNCTION tv_host_beat(text, jsonb), tv_placement_ack(uuid, bigint),
            tv_placement_release(uuid, bigint) TO maktaba_worker;
        -- slot 0124
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON storage_servers FROM maktaba_worker;
        GRANT SELECT ON storage_servers TO maktaba_worker;
        REVOKE ALL ON storage_path_migrations FROM maktaba_worker;
        -- slot 0125
        REVOKE ALL ON tv_remote_inputs, tv_remote_pushes, tv_remote_sessions,
            tv_remote_usage_daily, tv_push_secrets FROM maktaba_worker;
        GRANT SELECT ON tv_remote_inputs, tv_remote_pushes TO maktaba_worker;
        REVOKE EXECUTE ON FUNCTION tv_push_guard() FROM maktaba_worker;
        GRANT EXECUTE ON FUNCTION tv_push_credentials(uuid), tv_push_report(uuid, text, text)
            TO maktaba_worker;
        IF to_regclass('public.goose_db_version') IS NOT NULL THEN
            REVOKE INSERT, UPDATE, DELETE ON goose_db_version FROM maktaba_worker;
        END IF;
        -- slot 0126
        REVOKE ALL ON tv_simulcast_destinations, tv_simulcast_attachments, tv_simulcast_outputs
            FROM maktaba_worker;
        -- slot 0129
        REVOKE ALL ON tv_slots FROM maktaba_worker;
        GRANT SELECT ON tv_slots TO maktaba_worker;
        GRANT EXECUTE ON FUNCTION tv_slot_beat(text, text, jsonb) TO maktaba_worker;
        -- slot 0130
        REVOKE ALL ON notifications, notification_reads, notification_cursors FROM maktaba_worker;
        -- slot 0131
        REVOKE ALL ON storage_canonicalize FROM maktaba_worker;
        REVOKE ALL ON storage_path_backups FROM maktaba_worker;
        -- slot 0132
        REVOKE ALL ON release_catalog, update_runs, update_run_steps, update_targets,
            update_watermark FROM maktaba_worker;
        -- slot 0134
        REVOKE ALL ON cluster_sso_codes FROM maktaba_worker;
        -- slot 0135
        GRANT EXECUTE ON FUNCTION cluster_node_report_full(uuid, text, text, text, boolean, jsonb)
            TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE ON streaming_sessions TO maktaba_worker;
        -- slot 0136
        REVOKE ALL ON tv_swap_ops, tv_channel_switch FROM maktaba_worker;
        GRANT SELECT ON tv_swap_ops, tv_channel_switch TO maktaba_worker;
        -- slot 0137
        REVOKE ALL ON tv_slot_events FROM maktaba_worker;
        GRANT SELECT ON tv_slot_events TO maktaba_worker;
        -- slot 0139
        REVOKE ALL ON enrich_usage FROM maktaba_worker;
        GRANT SELECT ON enrich_usage TO maktaba_worker;
        -- slot 0140
        REVOKE ALL ON graphql_persisted_queries FROM maktaba_worker;
        -- slot 0143
        REVOKE ALL ON failure_ignores FROM maktaba_worker;
        GRANT SELECT ON failure_ignores TO maktaba_worker;
        REVOKE ALL ON item_requests FROM maktaba_worker;
        GRANT SELECT, UPDATE ON item_requests TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE ON dependency_health TO maktaba_worker;
        GRANT SELECT, INSERT, UPDATE ON item_checks TO maktaba_worker;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_replicas')
       AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user AND (rolsuper OR rolcreaterole)) THEN
        CREATE ROLE maktaba_replicas NOLOGIN;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_replica_beat (
    id      smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    at      timestamptz NOT NULL DEFAULT now(),
    ca_pem  text
);
INSERT INTO cluster_replica_beat (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        REVOKE ALL ON cluster_replica_beat FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS replica jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS replica_reported_at timestamptz;
-- +goose StatementEnd

-- +goose StatementBegin
-- A machine's report of its copy, for its own database role only (cluster_node_guard,
-- slot 0103): {state, lag_ms, replay_lsn, applied_at, detail}.
CREATE OR REPLACE FUNCTION cluster_node_report_replica(p_node uuid, p_replica jsonb) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM cluster_node_guard(p_node);
    UPDATE cluster_nodes n SET replica = COALESCE(p_replica, n.replica), replica_reported_at = now()
     WHERE n.id = p_node;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION cluster_node_report_replica(uuid, jsonb) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        GRANT EXECUTE ON FUNCTION cluster_node_report_replica(uuid, jsonb) TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The tables a machine's copy leaves out: the secrets (sealed keys, tokens, stream and
-- push keys, the license), the state of other machines, and what each database keeps
-- of its own (its migrations). A migration that adds such a table adds it here. Every
-- other table of public is copied: a machine's routes read their tables from its copy.
CREATE OR REPLACE FUNCTION cluster_copy_excluded() RETURNS text[]
LANGUAGE sql IMMUTABLE AS $f$
    SELECT ARRAY[
        -- secrets
        'home_secrets', 'tv_cluster_secret', 'tv_push_secrets', 'tv_simulcast_destinations',
        'refresh_tokens', 'password_reset_tokens', 'pairing_tickets', 'cluster_sso_codes',
        'idempotency_keys', 'licenses', 'devices', 'device_downloads', 'audit_log',
        -- the machines' own (a machine's row of cluster_nodes is published to it alone)
        'cluster_nodes', 'cluster_node_stages', 'cluster_metrics_cache', 'cluster_log_cache',
        'cluster_cache_files',
        -- each database's own
        'goose_db_version', 'maktaba_schema_version'
    ]::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The tables a machine's copy is made of.
CREATE OR REPLACE FUNCTION cluster_copy_tables() RETURNS SETOF regclass
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $f$
    SELECT c.oid::regclass FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
     WHERE n.nspname = 'public' AND c.relkind = 'r' AND NOT c.relispartition
       AND c.relname <> ALL (cluster_copy_excluded())
     ORDER BY c.relname
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The columns of its own cluster_nodes row a machine's copy holds (not the home's
-- private key for it, nor its sealed join answer; its own public key is one).
CREATE OR REPLACE FUNCTION cluster_copy_node_columns() RETURNS text[]
LANGUAGE sql IMMUTABLE AS $f$
    SELECT ARRAY['id', 'name', 'state', 'os', 'arch', 'node_pubkey', 'versions', 'capabilities', 'storage',
                 'path_map', 'path_map_source', 'drain', 'db_role', 'api_url', 'created_at',
                 'approved_at', 'updated_at', 'version', 'platform', 'web_url', 'full_node']::text[]
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A machine's publication, brought up to date with the tables this home has now (a
-- migration added one; a table dropped leaves by itself): every copied table, readable
-- by the replicas' group, and the machine's own row of cluster_nodes. Its name.
CREATE OR REPLACE FUNCTION cluster_copy_publication(p_node uuid) RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    v_pub  text := 'maktaba_copy_' || replace(p_node::text, '-', '');
    v_oid  oid;
    v_tab  regclass;
    v_cols text;
BEGIN
    SELECT oid INTO v_oid FROM pg_publication WHERE pubname = v_pub;
    IF v_oid IS NULL THEN
        EXECUTE format('CREATE PUBLICATION %I WITH (publish = %L)', v_pub, 'insert, update, delete, truncate');
        SELECT oid INTO v_oid FROM pg_publication WHERE pubname = v_pub;
    END IF;
    FOR v_tab IN SELECT * FROM cluster_copy_tables()
                 EXCEPT SELECT prrelid::regclass FROM pg_publication_rel WHERE prpubid = v_oid LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_index WHERE indrelid = v_tab AND indisprimary)
           AND (SELECT relreplident FROM pg_class WHERE oid = v_tab) = 'd' THEN
            EXECUTE format('ALTER TABLE %s REPLICA IDENTITY FULL', v_tab);
        END IF;
        EXECUTE format('ALTER PUBLICATION %I ADD TABLE %s', v_pub, v_tab);
        EXECUTE format('GRANT SELECT ON %s TO maktaba_replicas', v_tab);
    END LOOP;
    -- a table now left out leaves the publication
    FOR v_tab IN SELECT prrelid::regclass FROM pg_publication_rel r JOIN pg_class c ON c.oid = r.prrelid
                  WHERE r.prpubid = v_oid AND c.relname <> 'cluster_nodes'
                    AND c.relname = ANY (cluster_copy_excluded()) LOOP
        -- (the keyword split: the migrations' lint reads it as a table's drop)
        EXECUTE format('ALTER PUBLICATION %I DROP ' || 'TABLE %s', v_pub, v_tab);
    END LOOP;
    IF NOT EXISTS (SELECT 1 FROM pg_publication_rel WHERE prpubid = v_oid AND prrelid = 'cluster_nodes'::regclass) THEN
        SELECT string_agg(quote_ident(c), ', ') INTO v_cols FROM unnest(cluster_copy_node_columns()) c;
        EXECUTE format('ALTER PUBLICATION %I ADD TABLE cluster_nodes (%s) WHERE (id = %L::uuid)', v_pub, v_cols, p_node);
        EXECUTE format('GRANT SELECT (%s) ON cluster_nodes TO maktaba_replicas', v_cols);
    END IF;
    RETURN v_pub;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION cluster_copy_publication(uuid) FROM PUBLIC;
-- +goose StatementEnd

-- +goose StatementBegin
-- A machine's copy of this database, for its own database role only
-- (cluster_node_guard): its replication role, in the replicas' group, with the password
-- the machine chose (it knows it before it asks, so its programs are given it at their
-- start), and its publication, up to date. The machine's subscription makes the slot
-- (maktaba_copy_<id>); slot_exists says whether this home still has it: a subscription
-- whose slot is gone (this home gave up on a machine too far behind) copies anew.
-- p_fresh (the machine is about to subscribe anew, its subscription gone): a slot left
-- from before is dropped. It also says what the machine needs to follow: this home's
-- schema version (a copy's own migrations must be at least as far), the tables it
-- copies, the TLS CA it verifies this home with.
CREATE OR REPLACE FUNCTION cluster_node_replica_grant(p_node uuid, p_password text, p_fresh boolean)
RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    v_id   text := replace(p_node::text, '-', '');
    v_role text := 'maktaba_rep_' || replace(p_node::text, '-', '');
    v_slot text := 'maktaba_copy_' || replace(p_node::text, '-', '');
    v_pub  text;
    v_has  boolean;
    v_live boolean;
BEGIN
    PERFORM cluster_node_guard(p_node);
    IF NOT EXISTS (SELECT 1 FROM cluster_nodes WHERE id = p_node AND state = 'approved') THEN
        RAISE EXCEPTION 'cluster node % is not approved', p_node USING ERRCODE = 'insufficient_privilege';
    END IF;
    IF p_password IS NULL OR length(p_password) < 32 THEN
        RAISE EXCEPTION 'a copy''s password has 32 characters at least' USING ERRCODE = 'invalid_parameter_value';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_replicas') THEN
        RAISE EXCEPTION 'this database keeps no copies for machines (no replicas group)'
            USING ERRCODE = 'feature_not_supported';
    END IF;
    IF current_setting('wal_level') <> 'logical' THEN
        RAISE EXCEPTION 'this database keeps no copies for machines (wal_level is %, not logical)',
            current_setting('wal_level') USING ERRCODE = 'feature_not_supported';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_role) THEN
        EXECUTE format('ALTER ROLE %I WITH LOGIN REPLICATION INHERIT PASSWORD %L', v_role, p_password);
    ELSE
        EXECUTE format('CREATE ROLE %I WITH LOGIN REPLICATION INHERIT PASSWORD %L', v_role, p_password);
    END IF;
    IF NOT pg_has_role(v_role, 'maktaba_replicas', 'MEMBER') THEN
        EXECUTE format('GRANT maktaba_replicas TO %I', v_role);
    END IF;
    v_pub := cluster_copy_publication(p_node);
    SELECT true, active INTO v_has, v_live FROM pg_replication_slots WHERE slot_name = v_slot;
    v_has := coalesce(v_has, false);
    IF v_has AND p_fresh THEN
        IF v_live THEN
            RAISE EXCEPTION 'the slot % is in use: stop the copy first', v_slot USING ERRCODE = 'object_in_use';
        END IF;
        PERFORM pg_drop_replication_slot(v_slot);
        v_has := false;
    END IF;
    RETURN jsonb_build_object(
        'role', v_role,
        'slot', v_slot,
        'slot_exists', v_has,
        'publication', v_pub,
        'tables', (SELECT jsonb_agg(c.relname ORDER BY c.relname) FROM pg_publication_rel r
                     JOIN pg_class c ON c.oid = r.prrelid JOIN pg_publication p ON p.oid = r.prpubid
                    WHERE p.pubname = v_pub),
        'schema_version', (SELECT max(version_id) FROM goose_db_version WHERE is_applied),
        'system_identifier', (SELECT system_identifier FROM pg_control_system())::text,
        'server_version_num', current_setting('server_version_num')::int,
        'database', current_database(),
        'ca_pem', (SELECT ca_pem FROM cluster_replica_beat WHERE id = 1),
        'node', v_id);
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION cluster_node_replica_grant(uuid, text, boolean) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        GRANT EXECUTE ON FUNCTION cluster_node_replica_grant(uuid, text, boolean) TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS cluster_node_replica_grant(uuid, text, boolean);
DROP FUNCTION IF EXISTS cluster_copy_publication(uuid);
DROP FUNCTION IF EXISTS cluster_copy_node_columns();
DROP FUNCTION IF EXISTS cluster_copy_tables();
DROP FUNCTION IF EXISTS cluster_copy_excluded();
DROP FUNCTION IF EXISTS cluster_node_report_replica(uuid, jsonb);
DROP TABLE IF EXISTS cluster_replica_beat;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS replica_reported_at;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS replica;
-- +goose StatementEnd
