-- +goose Up
-- +goose StatementBegin
--
-- Slot 0219 - media permissions (Epic 59, story 59.03): what Maktaba may do in each
-- library, and subtitles kept in the database for a library it may only read.
--
--   libraries.access             rw | ro | none, the last check's result (the least access
--                                of the library's roots); NULL: never checked, which every
--                                writing feature treats as rw (how Maktaba behaved before).
--   libraries.access_checked_at  when the check ran (setup, a library saved, Check access,
--                                or after a write failed with a permission error; never at
--                                start: its probe write would wake a sleeping disk).
--   libraries.access_detail      each root's result ({roots: [{path, access, partial,
--                                unreadable, mount_read_only}]}), the writes that failed
--                                since ({write_failures: [{path, at}]}) and when the next
--                                read should check again (recheck_after).
--   subtitle_files.content       the subtitle's text when the library is read-only for
--                                Maktaba: the row is the file (path is where it would have
--                                been). Replicated with the table, so a node's replica and a
--                                node taking over the home have it too.
--
-- Additive: an older release never reads the columns.
--
ALTER TABLE libraries ADD COLUMN IF NOT EXISTS access text;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'libraries_access_valid') THEN
        ALTER TABLE libraries ADD CONSTRAINT libraries_access_valid
            CHECK (access IS NULL OR access IN ('rw', 'ro', 'none'));
    END IF;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries ADD COLUMN IF NOT EXISTS access_checked_at timestamptz;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries ADD COLUMN IF NOT EXISTS access_detail jsonb NOT NULL DEFAULT '{}'::jsonb;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE subtitle_files ADD COLUMN IF NOT EXISTS content text;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE subtitle_files DROP COLUMN IF EXISTS content;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries DROP COLUMN IF EXISTS access_detail;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries DROP COLUMN IF EXISTS access_checked_at;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries DROP CONSTRAINT IF EXISTS libraries_access_valid;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries DROP COLUMN IF EXISTS access;
-- +goose StatementEnd
