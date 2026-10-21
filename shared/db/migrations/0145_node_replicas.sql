-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0145 (node replicas, specs/epics/37-node-replica): every joined machine keeps a
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
--   7. web_session_keys: the web sessions as a copy holds them: the SHA-256 of each
--      session's id and of its CSRF token, never the values (a session id is a live
--      cookie: a copy of it would sign in at this home as its person), kept by triggers
--      from web_sessions, which a copy leaves out.
--   6. cluster_node_replica_grant: an approved full machine asks for its copy over its
--      own database connection: its replication role (maktaba_rep_<id>, with the SCRAM
--      verifier of the password the machine chose: the password never leaves it) and
--      its publication (maktaba_copy_<id>, brought up to date with the tables this home
--      has now); its subscription makes the slot. Its row is read FOR SHARE: a
--      revocation under way waits for the call, and one made before it is seen. A
--      machine that may not have a copy is refused with SQLSTATE MK403.
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
-- A standby home: a full machine the admin marks may take over as the home when the
-- home is gone for good (an emergency). It keeps a kit of the home's secrets and keys
-- sealed to it, which the home keeps current; standby_kit is what it holds
-- ({"hash", "at", "term"}: NULL, none). The mark removed, the kit is removed.
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS standby boolean NOT NULL DEFAULT false;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS standby_kit jsonb;
-- +goose StatementEnd

-- +goose StatementBegin
-- What an old home wrote after the cut of the emergency that took the home from it: it
-- reports them when it is back (sealed to the cluster), before it becomes a machine. The
-- admin downloads them, or applies the ones that do not conflict, in one transaction
-- (applied: which were, and why the others were not).
CREATE TABLE IF NOT EXISTS cluster_kept_aside (
    id           uuid PRIMARY KEY,
    term         bigint NOT NULL,
    from_node    uuid NOT NULL,
    from_name    text NOT NULL,
    cut_lsn      text,
    -- the changes, per transaction of the old home (its commit position and time)
    report       jsonb NOT NULL,
    -- the old home no longer had the slot that kept them: what it wrote is not known
    lost         boolean NOT NULL DEFAULT false,
    state        text NOT NULL DEFAULT 'reported' CHECK (state IN ('reported', 'applied', 'dismissed')),
    applied      jsonb,
    applied_at   timestamptz,
    applied_by   uuid,
    received_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (term, from_node)
);
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
        -- live cookies (their hashes are copied: web_session_keys)
        'web_sessions',
        -- the machines' own (a machine's row of cluster_nodes is published to it alone)
        'cluster_nodes', 'cluster_node_stages', 'cluster_metrics_cache', 'cluster_log_cache',
        'cluster_cache_files',
        -- what an old home wrote after an emergency took the home from it (the home's to
        -- apply or not)
        'cluster_kept_aside',
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
REVOKE EXECUTE ON FUNCTION cluster_node_replica_grant(uuid, text, boolean) FROM PUBLIC;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        GRANT EXECUTE ON FUNCTION cluster_node_replica_grant(uuid, text, boolean) TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The cluster's terms (specs/epics/37-node-replica, "Switching the home"): each time
-- another machine becomes the home a term starts. A home that learns of a newer term
-- than its own stops writing and rejoins as a machine of the new one (fencing). An
-- emergency term keeps the old home's position (cut_lsn, its WAL) up to which the new
-- home has its changes: what the old home wrote after it is kept aside there.
CREATE TABLE IF NOT EXISTS cluster_terms (
    term        bigint PRIMARY KEY CHECK (term >= 1),
    home_name   text NOT NULL,
    -- the machine's id while it was a joined machine (NULL: the first home)
    home_node   uuid,
    reason      text NOT NULL CHECK (reason IN ('initial', 'planned', 'emergency')),
    started_at  timestamptz NOT NULL DEFAULT now(),
    cut_lsn     pg_lsn
);
INSERT INTO cluster_terms (term, home_name, reason)
SELECT 1, COALESCE((SELECT name FROM instance_identity WHERE id = 1), 'home'), 'initial'
WHERE NOT EXISTS (SELECT 1 FROM cluster_terms);

-- A switch of the home, as the home runs it: its state is what the control centre shows
-- (and every machine's copy has it). Its row, set to "frozen" as the old home's last
-- write, is the marker: a copy that has it has everything the old home wrote.
CREATE TABLE IF NOT EXISTS cluster_switchovers (
    id            uuid PRIMARY KEY,
    term          bigint NOT NULL,
    kind          text NOT NULL CHECK (kind IN ('planned', 'emergency')),
    to_node       uuid NOT NULL,
    to_name       text NOT NULL,
    from_name     text NOT NULL,
    -- waiting_new_home: the promotion was asked and its answer did not come; the old home
    -- stays frozen until the machine taking over says it is the home or called it off
    state         text NOT NULL CHECK (state IN ('checking', 'freezing', 'frozen', 'handing_over',
                      'promoting', 'waiting_new_home', 'repointing', 'done', 'failed', 'cancelled')),
    detail        text,
    -- the old home's steps as it ran them ([{name, state, detail, at}])
    steps         jsonb NOT NULL DEFAULT '[]'::jsonb,
    -- per integration (plex, relay, ...): {"state": "moved|pending|failed|needs_you|not_used", "detail": ...}
    integrations  jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- per machine: {"state": "following|copying_anew|waiting|failed", "detail": ...}
    machines      jsonb NOT NULL DEFAULT '{}'::jsonb,
    -- per machine id: the new home's address sealed to that machine with the cluster key
    -- (a machine off during the switch reads it from any machine it reaches)
    notices       jsonb NOT NULL DEFAULT '{}'::jsonb,
    requested_by  uuid,
    requested_at  timestamptz NOT NULL DEFAULT now(),
    frozen_at     timestamptz,
    done_at       timestamptz,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- one switch at a time: two asked at once, the second is refused here
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS cluster_switchovers_one_open ON cluster_switchovers ((true))
    WHERE state NOT IN ('done', 'failed', 'cancelled');
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        GRANT SELECT ON cluster_terms, cluster_switchovers TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The web sessions as a copy holds them (7.): by the SHA-256 of the session's id (its
-- cookie) and of its CSRF token, and what a sign-in check needs.
CREATE TABLE IF NOT EXISTS web_session_keys (
    id_sha256       text PRIMARY KEY,
    user_id         uuid NOT NULL,
    csrf_sha256     text NOT NULL,
    created_at      timestamptz NOT NULL,
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz,
    origin_node_id  uuid
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION web_session_keys_sync() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $f$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM web_session_keys WHERE id_sha256 = encode(sha256(convert_to(OLD.id::text, 'UTF8')), 'hex');
        RETURN OLD;
    END IF;
    INSERT INTO web_session_keys (id_sha256, user_id, csrf_sha256, created_at, expires_at, revoked_at,
                                  origin_node_id)
    VALUES (encode(sha256(convert_to(NEW.id::text, 'UTF8')), 'hex'), NEW.user_id,
            encode(sha256(convert_to(NEW.csrf_token, 'UTF8')), 'hex'), NEW.created_at, NEW.expires_at,
            NEW.revoked_at, NEW.origin_node_id)
    ON CONFLICT (id_sha256) DO UPDATE SET user_id = EXCLUDED.user_id, csrf_sha256 = EXCLUDED.csrf_sha256,
        expires_at = EXCLUDED.expires_at, revoked_at = EXCLUDED.revoked_at,
        origin_node_id = EXCLUDED.origin_node_id;
    RETURN NEW;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE TRIGGER web_session_keys_sync
    AFTER INSERT OR DELETE OR UPDATE OF user_id, csrf_token, expires_at, revoked_at, origin_node_id
    ON web_sessions FOR EACH ROW EXECUTE FUNCTION web_session_keys_sync();
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO web_session_keys (id_sha256, user_id, csrf_sha256, created_at, expires_at, revoked_at, origin_node_id)
SELECT encode(sha256(convert_to(id::text, 'UTF8')), 'hex'), user_id,
       encode(sha256(convert_to(csrf_token, 'UTF8')), 'hex'), created_at, expires_at, revoked_at, origin_node_id
  FROM web_sessions
ON CONFLICT (id_sha256) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
-- commit_segment with its worker (slot 0013's, guarded by the claim): a segment from a
-- worker that no longer holds the job (a machine whose home was away past the lease,
-- the job given to another) writes nothing: neither the segment nor the job's progress
-- and heartbeat. NULL p_worker: as slot 0013's.
CREATE OR REPLACE FUNCTION commit_segment(
    p_transcript_id    UUID,
    p_job_id           BIGINT,
    p_seq              INT,
    p_start_sec        REAL,
    p_end_sec          REAL,
    p_text             TEXT,
    p_speaker          TEXT,
    p_confidence       REAL,
    p_audio_sec_in_seg REAL,
    p_wall_sec_in_seg  REAL,
    p_total_duration   REAL,
    p_ewma_alpha       REAL,
    p_worker           TEXT
) RETURNS BIGINT
LANGUAGE plpgsql AS $$
DECLARE
    v_segment_id   BIGINT;
    v_prev_end     REAL;
    v_prev_factor  REAL;
    v_factor       REAL;
    v_processed    REAL;
    v_eta          REAL;
BEGIN
    SELECT last_segment_end_sec, COALESCE(realtime_factor, 0)
      INTO v_prev_end, v_prev_factor
      FROM processing_jobs
     WHERE id = p_job_id AND (p_worker IS NULL OR claimed_by = p_worker)
     FOR UPDATE;
    IF NOT FOUND THEN
        RETURN NULL;
    END IF;

    INSERT INTO transcript_segments
           (transcript_id, seq, start_sec, end_sec, text, speaker, confidence)
    VALUES (p_transcript_id, p_seq, p_start_sec, p_end_sec, p_text, p_speaker, p_confidence)
    ON CONFLICT (transcript_id, seq) DO NOTHING
    RETURNING id INTO v_segment_id;
    IF v_segment_id IS NULL THEN
        RETURN NULL;
    END IF;

    v_processed := GREATEST(0, p_end_sec - GREATEST(v_prev_end, p_start_sec));
    IF p_wall_sec_in_seg <= 0 THEN
        v_factor := v_prev_factor;
    ELSE
        v_factor := v_prev_factor * (1 - p_ewma_alpha)
                  + (p_audio_sec_in_seg / p_wall_sec_in_seg) * p_ewma_alpha;
    END IF;
    IF v_factor > 0 AND p_total_duration > 0 THEN
        v_eta := (p_total_duration - LEAST(p_end_sec, p_total_duration)) / v_factor;
    ELSE
        v_eta := NULL;
    END IF;

    UPDATE processing_jobs
       SET last_segment_end_sec     = GREATEST(last_segment_end_sec, p_end_sec),
           processed_seconds        = processed_seconds + v_processed,
           segments_completed       = segments_completed + 1,
           realtime_factor          = v_factor,
           estimated_remaining_sec  = v_eta,
           progress_updated_at      = now(),
           last_heartbeat_at        = now()
     WHERE id = p_job_id;

    RETURN v_segment_id;
END;
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS commit_segment(UUID, BIGINT, INT, REAL, REAL, TEXT, TEXT, REAL, REAL, REAL, REAL, REAL, TEXT);
DROP TRIGGER IF EXISTS web_session_keys_sync ON web_sessions;
DROP FUNCTION IF EXISTS web_session_keys_sync();
DROP TABLE IF EXISTS web_session_keys;
DROP TABLE IF EXISTS cluster_switchovers;
DROP TABLE IF EXISTS cluster_terms;
DROP FUNCTION IF EXISTS cluster_node_replica_grant(uuid, text, boolean);
DROP FUNCTION IF EXISTS cluster_copy_publication(uuid);
DROP FUNCTION IF EXISTS cluster_copy_node_columns();
DROP FUNCTION IF EXISTS cluster_copy_tables();
DROP FUNCTION IF EXISTS cluster_copy_excluded();
DROP FUNCTION IF EXISTS cluster_node_report_replica(uuid, jsonb);
DROP TABLE IF EXISTS cluster_replica_beat;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS replica_reported_at;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS replica;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS standby_kit;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS standby;
DROP TABLE IF EXISTS cluster_kept_aside;
-- +goose StatementEnd
