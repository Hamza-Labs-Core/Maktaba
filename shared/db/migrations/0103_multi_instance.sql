-- +goose Up
-- +goose StatementBegin
--
-- Slot 0103 (Epic 31) - multi-instance Maktaba: one home (this database and queue),
-- other machines on the LAN join it as workers after an admin approves them with a
-- pairing code (specs/epics/31-multi-instance/design.md). The home's identity and its
-- "accept new machines" switch; the machines and their per-stage switches; who a worker
-- is (home or node) and why it does not claim a stage; where a stage may run.
--
CREATE TABLE IF NOT EXISTS instance_identity (
    id                  smallint PRIMARY KEY CHECK (id = 1),
    instance_id         uuid NOT NULL DEFAULT gen_random_uuid(),
    name                text NOT NULL,
    accept_joins        boolean NOT NULL DEFAULT false,
    accept_joins_until  timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_nodes (
    id               uuid PRIMARY KEY,
    name             text NOT NULL,
    state            text NOT NULL DEFAULT 'pending'
                     CHECK (state IN ('pending', 'approved', 'rejected', 'revoked')),
    os               text,
    arch             text,
    versions         jsonb NOT NULL DEFAULT '{}'::jsonb,
    capabilities     jsonb NOT NULL DEFAULT '{}'::jsonb,
    storage          jsonb NOT NULL DEFAULT '{}'::jsonb,
    path_map         jsonb,
    path_map_source  text CHECK (path_map_source IN ('inferred', 'admin')),
    drain            boolean NOT NULL DEFAULT false,
    node_pubkey      text NOT NULL,
    home_pubkey      text,
    home_privkey     text,
    code_attempts    integer NOT NULL DEFAULT 0,
    expires_at       timestamptz,
    sealed           jsonb,
    db_role          text,
    remote_addr      text,
    api_url          text,             -- the API URL the machine used to ask (its database host)
    created_at       timestamptz NOT NULL DEFAULT now(),
    approved_at      timestamptz,
    approved_by      text,
    revoked_at       timestamptz,
    last_seen_at     timestamptz,
    updated_at       timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS cluster_node_stages (
    node_id  uuid NOT NULL REFERENCES cluster_nodes(id) ON DELETE CASCADE,
    stage    text NOT NULL REFERENCES job_stages(stage) ON DELETE CASCADE,
    enabled  boolean NOT NULL,
    PRIMARY KEY (node_id, stage)
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE workers ADD COLUMN IF NOT EXISTS node_id uuid;
ALTER TABLE workers ADD COLUMN IF NOT EXISTS role text NOT NULL DEFAULT 'home';
ALTER TABLE workers ADD COLUMN IF NOT EXISTS os text;
ALTER TABLE workers ADD COLUMN IF NOT EXISTS capabilities jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE workers ADD COLUMN IF NOT EXISTS stages_off jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE job_stages ADD COLUMN IF NOT EXISTS placement text NOT NULL DEFAULT 'any';
UPDATE job_stages SET placement = 'home' WHERE stage IN ('index', 'thumb_small', 'review_pages');
-- +goose StatementEnd

-- +goose StatementBegin
-- The machines' database roles join this group: data, no schema. Created only when the
-- migrating user may create roles; otherwise the home reports joins as unavailable.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user
               AND (rolsuper OR rolcreaterole)) THEN
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
            CREATE ROLE maktaba_worker NOLOGIN;
        END IF;
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
        -- what a worker never needs: accounts, sessions, tokens, licences, devices,
        -- viewing data (the audit log only takes new rows), and the cluster tables
        -- (a machine sees its own row through the functions below, never another's
        -- sealed credentials or the home's pairing keys). A later migration adding a
        -- table of secrets revokes it here the same way: default privileges grant data
        -- access to new tables.
        REVOKE ALL ON users, web_sessions, refresh_tokens, personal_access_tokens,
            password_reset_tokens, pairing_tickets, licenses, devices, device_downloads,
            streaming_sessions, watch_sessions, user_recs, user_analytics_prefs,
            idempotency_keys, instance_identity, cluster_nodes, cluster_node_stages
            FROM maktaba_worker;
        REVOKE ALL ON audit_log FROM maktaba_worker;
        GRANT INSERT ON audit_log TO maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A machine's own row, for its own database role only (cluster_nodes.db_role): its
-- heartbeat (state, drain, path map), its report (capabilities, storage, inferred path
-- map), its stage switches. The home's own user (not a maktaba_worker member) may call
-- them for any machine.
CREATE OR REPLACE FUNCTION cluster_node_guard(p_node uuid) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    IF pg_has_role(session_user, 'maktaba_worker', 'MEMBER')
       AND NOT EXISTS (SELECT 1 FROM cluster_nodes
                       WHERE id = p_node AND db_role = session_user) THEN
        RAISE EXCEPTION 'cluster node % is not this login''s', p_node
            USING ERRCODE = 'insufficient_privilege';
    END IF;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cluster_node_beat(p_node uuid)
RETURNS TABLE (state text, drain boolean, path_map jsonb, path_map_source text)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM cluster_node_guard(p_node);
    RETURN QUERY
    UPDATE cluster_nodes n SET last_seen_at = now()
     WHERE n.id = p_node
    RETURNING n.state, n.drain, n.path_map, n.path_map_source;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cluster_node_report(
    p_node uuid, p_capabilities jsonb, p_storage jsonb, p_path_map jsonb
) RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM cluster_node_guard(p_node);
    UPDATE cluster_nodes n SET
        capabilities = COALESCE(p_capabilities, n.capabilities),
        storage = COALESCE(p_storage, n.storage),
        -- an admin's map is never replaced by an inferred one
        path_map = CASE WHEN p_path_map IS NOT NULL
                         AND n.path_map_source IS DISTINCT FROM 'admin'
                        THEN p_path_map ELSE n.path_map END,
        path_map_source = CASE WHEN p_path_map IS NOT NULL
                                AND n.path_map_source IS DISTINCT FROM 'admin'
                               THEN 'inferred' ELSE n.path_map_source END,
        updated_at = now()
     WHERE n.id = p_node;
END
$f$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION cluster_node_stage_switches(p_node uuid)
RETURNS TABLE (stage text, enabled boolean)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $f$
BEGIN
    PERFORM cluster_node_guard(p_node);
    RETURN QUERY SELECT s.stage, s.enabled FROM cluster_node_stages s WHERE s.node_id = p_node;
END
$f$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS cluster_node_stage_switches(uuid);
DROP FUNCTION IF EXISTS cluster_node_report(uuid, jsonb, jsonb, jsonb);
DROP FUNCTION IF EXISTS cluster_node_beat(uuid);
DROP FUNCTION IF EXISTS cluster_node_guard(uuid);
ALTER TABLE job_stages DROP COLUMN IF EXISTS placement;
ALTER TABLE workers DROP COLUMN IF EXISTS stages_off;
ALTER TABLE workers DROP COLUMN IF EXISTS capabilities;
ALTER TABLE workers DROP COLUMN IF EXISTS os;
ALTER TABLE workers DROP COLUMN IF EXISTS role;
ALTER TABLE workers DROP COLUMN IF EXISTS node_id;
DROP TABLE IF EXISTS cluster_node_stages;
DROP TABLE IF EXISTS cluster_nodes;
DROP TABLE IF EXISTS instance_identity;
-- +goose StatementEnd
