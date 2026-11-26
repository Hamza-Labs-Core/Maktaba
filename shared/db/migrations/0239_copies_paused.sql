-- +goose Up
--
-- Slot 0239 (#1667): let joined machines tell when the home paused their copies for its
-- own migrations (pg_hba.conf rejects /^maktaba_rep_) rather than counting login refusals
-- and backing off up to 30 minutes, and set server-side TCP keepalives on each machine's
-- replication role so a sleeping laptop's stuck COPY ends within ~60 s on operator
-- Postgres as well as Maktaba's bundled Postgres.
--
-- Additive: adds cluster_copies_paused() and extends cluster_node_replica_grant()'s JSON
-- answer with copies_paused plus ALTER ROLE ... SET tcp_keepalives_*.
--
-- +goose StatementBegin
-- Reports whether this home currently rejects the machines' replication roles in
-- pg_hba.conf (pgbundle.PauseCopies). Returns false when the app role lacks SELECT on
-- pg_catalog.pg_hba_file_rules (operator Postgres without the grant).
CREATE OR REPLACE FUNCTION cluster_copies_paused() RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF NOT has_table_privilege('pg_catalog.pg_hba_file_rules', 'SELECT') THEN
        RETURN false;
    END IF;
    RETURN EXISTS (
        SELECT 1 FROM pg_catalog.pg_hba_file_rules
         WHERE error IS NULL
           AND auth_method = 'reject'
           AND '/^maktaba_rep_' = ANY (user_name)
    );
EXCEPTION WHEN insufficient_privilege THEN
    RETURN false;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE EXECUTE ON FUNCTION cluster_copies_paused() FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        GRANT EXECUTE ON FUNCTION cluster_copies_paused() TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Slot 0147's cluster_node_replica_grant, extended to configure TCP keepalives on each
-- machine's replication role (PGC_USERSET: drops dead COPY sessions within ~60 s even on
-- operator Postgres) and report copies_paused in the returned grant JSON.
CREATE OR REPLACE FUNCTION cluster_node_replica_grant(p_node uuid, p_verifier text, p_fresh boolean)
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
    PERFORM 1 FROM cluster_nodes WHERE id = p_node AND state = 'approved' AND full_node FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'cluster node % may not have a copy (not approved, or not a full machine)', p_node
            USING ERRCODE = 'MK403';
    END IF;
    IF p_verifier IS NULL
       OR p_verifier !~ '^SCRAM-SHA-256\$[0-9]+:[A-Za-z0-9+/=]+\$[A-Za-z0-9+/=]+:[A-Za-z0-9+/=]+$' THEN
        RAISE EXCEPTION 'a copy''s password is given as its SCRAM-SHA-256 verifier'
            USING ERRCODE = 'invalid_parameter_value';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_replicas') THEN
        RAISE EXCEPTION 'this database keeps no copies for machines (no replicas group)'
            USING ERRCODE = 'feature_not_supported';
    END IF;
    IF current_setting('wal_level') <> 'logical' THEN
        RAISE EXCEPTION 'this database keeps no copies for machines (wal_level is %, not logical)',
            current_setting('wal_level') USING ERRCODE = 'feature_not_supported';
    END IF;
    -- a machine's own row is published by a row filter, without its keys by a column list
    IF current_setting('server_version_num')::int < 150000 THEN
        RAISE EXCEPTION 'this database keeps no copies for machines (Postgres %: they need 15 or later)',
            current_setting('server_version') USING ERRCODE = 'feature_not_supported';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_role) THEN
        EXECUTE format('ALTER ROLE %I WITH LOGIN REPLICATION INHERIT PASSWORD %L', v_role, p_verifier);
    ELSE
        EXECUTE format('CREATE ROLE %I WITH LOGIN REPLICATION INHERIT PASSWORD %L', v_role, p_verifier);
    END IF;
    EXECUTE format('ALTER ROLE %I SET tcp_keepalives_idle = 30', v_role);
    EXECUTE format('ALTER ROLE %I SET tcp_keepalives_interval = 10', v_role);
    EXECUTE format('ALTER ROLE %I SET tcp_keepalives_count = 3', v_role);
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
        'copies_paused', cluster_copies_paused(),
        'node', v_id);
END
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cluster_node_replica_grant(p_node uuid, p_verifier text, p_fresh boolean)
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
    PERFORM 1 FROM cluster_nodes WHERE id = p_node AND state = 'approved' AND full_node FOR SHARE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'cluster node % may not have a copy (not approved, or not a full machine)', p_node
            USING ERRCODE = 'MK403';
    END IF;
    IF p_verifier IS NULL
       OR p_verifier !~ '^SCRAM-SHA-256\$[0-9]+:[A-Za-z0-9+/=]+\$[A-Za-z0-9+/=]+:[A-Za-z0-9+/=]+$' THEN
        RAISE EXCEPTION 'a copy''s password is given as its SCRAM-SHA-256 verifier'
            USING ERRCODE = 'invalid_parameter_value';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_replicas') THEN
        RAISE EXCEPTION 'this database keeps no copies for machines (no replicas group)'
            USING ERRCODE = 'feature_not_supported';
    END IF;
    IF current_setting('wal_level') <> 'logical' THEN
        RAISE EXCEPTION 'this database keeps no copies for machines (wal_level is %, not logical)',
            current_setting('wal_level') USING ERRCODE = 'feature_not_supported';
    END IF;
    -- a machine's own row is published by a row filter, without its keys by a column list
    IF current_setting('server_version_num')::int < 150000 THEN
        RAISE EXCEPTION 'this database keeps no copies for machines (Postgres %: they need 15 or later)',
            current_setting('server_version') USING ERRCODE = 'feature_not_supported';
    END IF;
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = v_role) THEN
        EXECUTE format('ALTER ROLE %I WITH LOGIN REPLICATION INHERIT PASSWORD %L', v_role, p_verifier);
    ELSE
        EXECUTE format('CREATE ROLE %I WITH LOGIN REPLICATION INHERIT PASSWORD %L', v_role, p_verifier);
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
DROP FUNCTION IF EXISTS cluster_copies_paused();
-- +goose StatementEnd
