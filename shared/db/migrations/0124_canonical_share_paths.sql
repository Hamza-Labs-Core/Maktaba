-- +goose Up
-- +goose StatementBegin
--
-- Slot 0124 (Epic 31 / story 31.3) - media paths in a canonical, machine-independent
-- form: smb://<server>/<share>/<path> (specs/epics/31-multi-instance/story-31-03-canonical-share-paths.md).
--
-- storage_servers: the identity a canonical path names. A path stores the server's
-- name ("nas"), never an address; the addresses (IPs, host names, the mDNS name) live
-- here, so a new IP is one edit and no stored path changes. char_map: how the server's
-- on-disk names differ from what an SMB client sees ('none' | 'fullwidth' | 'sfm' |
-- a catia list '0x22:0xff02,...'). exports: NFS export path -> share name.
--
CREATE TABLE IF NOT EXISTS storage_servers (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE
                CHECK (name = lower(name) AND name <> '' AND position('/' in name) = 0),
    addresses   jsonb NOT NULL DEFAULT '[]'::jsonb,
    char_map    text NOT NULL DEFAULT 'none',
    exports     jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- One row per value the path migration changed (maktaba-pipeline storage canonicalize):
-- what it was, what it became, on which machine's mounts. "storage revert" puts the old
-- value back where the column still holds the new one.
CREATE TABLE IF NOT EXISTS storage_path_migrations (
    batch_id     uuid NOT NULL,
    table_name   text NOT NULL,
    column_name  text NOT NULL,
    row_key      text NOT NULL,
    old_value    text NOT NULL,
    new_value    text NOT NULL,
    machine      text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    reverted_at  timestamptz,
    PRIMARY KEY (batch_id, table_name, column_name, row_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- The home's own storage check (per share) and its admin "share -> local mount"
-- override, as cluster_nodes has them for the joined machines.
ALTER TABLE instance_identity ADD COLUMN IF NOT EXISTS storage jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE instance_identity ADD COLUMN IF NOT EXISTS path_map jsonb;
-- +goose StatementEnd

-- +goose StatementBegin
-- A machine reads the servers (to resolve paths) but never edits them or the
-- migration record.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON storage_servers FROM maktaba_worker;
        GRANT SELECT ON storage_servers TO maktaba_worker;
        REVOKE ALL ON storage_path_migrations FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE instance_identity DROP COLUMN IF EXISTS path_map;
ALTER TABLE instance_identity DROP COLUMN IF EXISTS storage;
DROP TABLE IF EXISTS storage_path_migrations;
DROP TABLE IF EXISTS storage_servers;
-- +goose StatementEnd
