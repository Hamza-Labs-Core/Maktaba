-- +goose Up
-- +goose StatementBegin
--
-- Slot 0142 (node replicas, specs/epics/37-node-replica): every joined machine keeps a
-- live read-only copy of this database and runs on it when the home is away.
--
--   1. The machines' group (maktaba_worker) on a home whose migrations ran without the
--      right to create roles (the Postgres bundled with Maktaba.app, whose app role
--      could not before its cluster bootstrap): created now that it can, with the
--      privileges slots 0103..0141 give it, replayed in their order. A home that has
--      the group is not touched.
--   2. The replicas' group (maktaba_replicas): each machine's replication role joins
--      it; the home's pg_hba lets the group stream its copy, nothing else. The group
--      reads everything (pg_read_all_data, granted where the migrating user may: the
--      bundled Postgres grants it at its cluster bootstrap): a machine reads its copy,
--      sign-ins included, as its replication role, and that role streams every change
--      of this database already.
--   3. cluster_nodes.replica: what a machine reports of its copy (state, lag, replay
--      position, when it last applied, what it is doing: copying, rebuilding), through
--      cluster_node_report_replica.
--   4. cluster_replica_beat: this home's clock, written every few seconds while a
--      machine follows it, so a copy knows how far it has applied the home's changes
--      (the offline sign-in's freshness rule, the lag the machines page shows); and
--      the CA of this database's TLS, which a machine pins for its replication.
--   5. cluster_node_replica_grant: a machine asks for its copy over its own database
--      connection: its replication role (maktaba_rep_<id>, with the password the
--      machine chose) and its slot (maktaba_copy_<id>).
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
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_replicas')
       AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user AND (rolsuper OR rolcreaterole)) THEN
        CREATE ROLE maktaba_replicas NOLOGIN;
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_replicas')
       AND EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user AND rolsuper) THEN
        GRANT pg_read_all_data TO maktaba_replicas;
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
-- A machine's copy of this database, for its own database role only
-- (cluster_node_guard): its replication role, in the replicas' group, with the password
-- the machine chose (it knows it before it asks, so its programs are given it at their
-- start), and its slot, which keeps the changes the machine has not applied yet.
-- p_fresh (the machine is about to copy the database anew, its copy stopped): the slot
-- is made anew, from now. A slot this home gave up on (past the changes it keeps for a
-- machine behind) is made anew too, and slot_made says so: the machine's copy cannot
-- follow from where it was and copies the database anew.
CREATE OR REPLACE FUNCTION cluster_node_replica_grant(p_node uuid, p_password text, p_fresh boolean)
RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
DECLARE
    v_id   text := replace(p_node::text, '-', '');
    v_role text := 'maktaba_rep_' || replace(p_node::text, '-', '');
    v_slot text := 'maktaba_copy_' || replace(p_node::text, '-', '');
    v_has  boolean;
    v_live boolean;
    v_lost boolean;
    v_made boolean := false;
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
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_role) THEN
        EXECUTE format('ALTER ROLE %I WITH LOGIN REPLICATION INHERIT PASSWORD %L', v_role, p_password);
    ELSE
        EXECUTE format('CREATE ROLE %I WITH LOGIN REPLICATION INHERIT PASSWORD %L', v_role, p_password);
    END IF;
    IF NOT pg_has_role(v_role, 'maktaba_replicas', 'MEMBER') THEN
        EXECUTE format('GRANT maktaba_replicas TO %I', v_role);
    END IF;
    SELECT true, active, coalesce(wal_status = 'lost', false) OR slot_type <> 'physical'
      INTO v_has, v_live, v_lost
      FROM pg_replication_slots WHERE slot_name = v_slot;
    v_has := coalesce(v_has, false);
    IF v_has AND (p_fresh OR v_lost) THEN
        IF v_live THEN
            RAISE EXCEPTION 'the slot % is in use: stop the copy first', v_slot USING ERRCODE = 'object_in_use';
        END IF;
        PERFORM pg_drop_replication_slot(v_slot);
        v_has := false;
    END IF;
    IF NOT v_has THEN
        PERFORM pg_create_physical_replication_slot(v_slot, true);
        v_made := true;
    END IF;
    RETURN jsonb_build_object(
        'role', v_role,
        'slot', v_slot,
        'slot_made', v_made,
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
DROP FUNCTION IF EXISTS cluster_node_report_replica(uuid, jsonb);
DROP TABLE IF EXISTS cluster_replica_beat;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS replica_reported_at;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS replica;
-- +goose StatementEnd
