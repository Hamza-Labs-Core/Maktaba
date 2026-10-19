-- +goose Up
-- +goose StatementBegin
--
-- Slot 0134 (Epic 35 story 35.9, README slot S2): sessions across the machines of a
-- cluster (specs/epics/35-cluster-full-nodes-and-updates/design.md §10). Sessions stay in
-- the home's web_sessions, resolved only by the home; a full node asks the home and
-- caches the answer.
--
-- web_sessions.origin_node_id: the machine a session was created through (a sign-in on
-- a node is a sign-in on the home, tagged with the node); NULL: the home itself.
-- web_sessions.parent_session_id: a cluster sign-in's child session (one per machine
-- the user continued to); signing out on the home ends its children.
--
ALTER TABLE web_sessions ADD COLUMN IF NOT EXISTS origin_node_id uuid;
ALTER TABLE web_sessions ADD COLUMN IF NOT EXISTS parent_session_id uuid
    REFERENCES web_sessions(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
-- The cluster sign-in's one-time codes (design §10.5): stored hashed, 60 s, single use,
-- bound to the target machine and a PKCE S256 challenge.
CREATE TABLE IF NOT EXISTS cluster_sso_codes (
    code_hash          text PRIMARY KEY,
    node_id            uuid NOT NULL REFERENCES cluster_nodes(id) ON DELETE CASCADE,
    user_id            uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_session_id  uuid NOT NULL REFERENCES web_sessions(id) ON DELETE CASCADE,
    pkce_challenge     text NOT NULL,
    return_path        text NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    expires_at         timestamptz NOT NULL,
    used_at            timestamptz
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Which delegation key a machine holds (the keys themselves are sealed in home_secrets
-- as cluster.node.<id>.delegation, current + previous) and when it was last rotated.
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS delegation_key_id text;
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS delegation_rotated_at timestamptz;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        -- slot 0103's default privileges gave the group role rights on new tables: only
        -- the home's API reads or writes the sign-in codes.
        REVOKE ALL ON cluster_sso_codes FROM maktaba_worker;
    END IF;
    -- A full node's API refuses to start when its role may create objects in the
    -- schema (design §9.1). Before PostgreSQL 15 every role may (CREATE on schema public
    -- is granted to PUBLIC); take it back as PostgreSQL 15 does by default, keeping it
    -- for the user that runs the migrations (the home's own).
    IF current_setting('server_version_num')::int < 150000
       AND (EXISTS (SELECT 1 FROM pg_roles WHERE rolname = current_user AND rolsuper)
            OR EXISTS (SELECT 1 FROM pg_namespace n JOIN pg_roles r ON r.oid = n.nspowner
                       WHERE n.nspname = 'public' AND r.rolname = current_user)) THEN
        REVOKE CREATE ON SCHEMA public FROM PUBLIC;
        EXECUTE format('GRANT CREATE ON SCHEMA public TO %I', current_user);
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS delegation_rotated_at;
ALTER TABLE cluster_nodes DROP COLUMN IF EXISTS delegation_key_id;
DROP TABLE IF EXISTS cluster_sso_codes;
ALTER TABLE web_sessions DROP COLUMN IF EXISTS parent_session_id;
ALTER TABLE web_sessions DROP COLUMN IF EXISTS origin_node_id;
-- +goose StatementEnd
