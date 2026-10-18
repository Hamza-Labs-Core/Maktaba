-- +goose NO TRANSACTION
-- +goose Up
--
-- slot 0114 (Epic 27, dizqueTV parity): what Maktaba's own scheduler needs to air
-- what the dizqueTV channels air.
--
--   channel_programs.kind        admits 'live': a live broadcast (a YouTube live page or
--                                a stream address) the channel carries at set times;
--   channel_programs.source_url  that broadcast's address (NULL for every other kind);
--   filler_pools.external_key    the channel plan's pool key ("<channel>.topic",
--                                "library.general" ...) of a pool write_channels made:
--                                its idempotent upsert key, and the mark of a row the
--                                tool owns (a pool without one was made by hand and is
--                                never touched);
--   job_stages write_channels 2  (filler pools, the channel's look): the version sweep
--                                writes every planned channel once more.
--
-- NO TRANSACTION: the unique index is built CONCURRENTLY; every statement is
-- idempotent on its own.
--
-- +goose StatementBegin
ALTER TABLE channel_programs DROP CONSTRAINT IF EXISTS channel_programs_kind_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'channel_programs_kind_check'
          AND conrelid = 'channel_programs'::regclass
    ) THEN
        ALTER TABLE channel_programs
            ADD CONSTRAINT channel_programs_kind_check
            CHECK (kind IN ('program', 'filler', 'bumper', 'slate', 'live'));
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_programs ADD COLUMN IF NOT EXISTS source_url TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE filler_pools ADD COLUMN IF NOT EXISTS external_key TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS filler_pools_external_key_uniq
    ON filler_pools (external_key) WHERE external_key IS NOT NULL;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET version = 2 WHERE stage = 'write_channels';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 1 WHERE stage = 'write_channels';
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS filler_pools_external_key_uniq;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE filler_pools DROP COLUMN IF EXISTS external_key;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM channel_programs WHERE kind = 'live';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_programs DROP COLUMN IF EXISTS source_url;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_programs DROP CONSTRAINT IF EXISTS channel_programs_kind_check;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'channel_programs_kind_check'
          AND conrelid = 'channel_programs'::regclass
    ) THEN
        ALTER TABLE channel_programs
            ADD CONSTRAINT channel_programs_kind_check
            CHECK (kind IN ('program', 'filler', 'bumper', 'slate'));
    END IF;
END
$$;
-- +goose StatementEnd
