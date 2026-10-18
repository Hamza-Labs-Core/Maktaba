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
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
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
