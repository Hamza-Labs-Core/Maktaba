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
--      it; the home's pg_hba lets the group stream its copy, nothing else.
--   3. cluster_nodes.replica: what a machine reports of its copy (state, lag, replay
--      position, when it last applied, what it is doing: copying, rebuilding), through
--      cluster_node_report_replica.
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

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS cluster_node_report_replica(uuid, jsonb);
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS replica_reported_at;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS replica;
-- +goose StatementEnd
