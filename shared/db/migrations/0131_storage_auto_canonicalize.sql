-- +goose Up
-- +goose StatementBegin
--
-- Slot 0131 (Epic 31 / story 31.3): the home converts the stored media paths to the
-- canonical form by itself (specs/epics/31-multi-instance/story-31-03-canonical-share-paths.md,
-- "What happens automatically").
--
-- storage_canonicalize: one row, the automatic conversion's state as the home's
-- pipeline keeps it (phase, why it waits, progress, the batch and backup of the run,
-- the servers it registered) and the admin's request from the Machines page
-- ('run' = check now, 'revert' = undo the last conversion, 'resume' = convert
-- automatically again after a revert).
--
CREATE TABLE IF NOT EXISTS storage_canonicalize (
    id            integer PRIMARY KEY CHECK (id = 1),
    state         jsonb NOT NULL DEFAULT '{}'::jsonb,
    request       text CHECK (request IS NULL OR request IN ('run', 'revert', 'resume')),
    requested_at  timestamptz,
    requested_by  text,
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO storage_canonicalize (id) VALUES (1) ON CONFLICT (id) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
-- The copy of every path column the conversion touches, taken before it writes
-- (one backup per run; the home keeps the last few). "storage restore-backup" puts a
-- backup's values back without any shell access to the database.
CREATE TABLE IF NOT EXISTS storage_path_backups (
    backup_id    uuid NOT NULL,
    table_name   text NOT NULL,
    column_name  text NOT NULL,
    row_key      text NOT NULL,
    value        text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (backup_id, table_name, column_name, row_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- the home-only stage that runs the conversion (engine/graph.py), seeded so a fresh
-- database matches the graph before the first worker syncs it
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode, placement)
VALUES
    ('storage_canonicalize', 'Canonical share paths', ARRAY['catalog']::text[], 'cpu', 'media', 11, 1, 0, 0, 1, '[]'::jsonb, '{}'::text[], '{}'::text[], false, false, false, 'auto', 'home')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
-- the machines never see or change the conversion's state or the backups
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'maktaba_worker') THEN
        REVOKE ALL ON storage_canonicalize FROM maktaba_worker;
        REVOKE ALL ON storage_path_backups FROM maktaba_worker;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM job_stages WHERE stage = 'storage_canonicalize';
DROP TABLE IF EXISTS storage_path_backups;
DROP TABLE IF EXISTS storage_canonicalize;
-- +goose StatementEnd
