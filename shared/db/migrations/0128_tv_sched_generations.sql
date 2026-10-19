-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
-- Slot 0128 (Epic 33, story 33.3) - schedule generations
-- (specs/epics/33-tv-staging-hot-swap/design.md section 4).
--
-- channel_programs.sched_gen: every block belongs to one schedule generation; every row
-- that exists today is generation 0. A staged TV change writes generation N+1 next to
-- the live one (a copy of N up to each channel's divergence boundary, regenerated after
-- it); the swap flips tv_routing.live_gen. The column has a constant default, so adding
-- it is a metadata-only change (Postgres 11+). The new unique (channel, gen, seq) and
-- time (channel, gen, start, end) indexes are built concurrently, then the old ones go.
--
-- channel_schedule_state is per (channel, generation): each generation has its own
-- cursor, horizon and stale flag (shuffle bags and marathon cursors diverge after the
-- boundary). The primary key is swapped onto a concurrently built unique index.
--
-- channels.lifecycle: 'staged' (a channel only a staged release has: it needs a row for
-- the programmes' foreign key; it is written disabled, its real values are in
-- tv_release_channels.def), 'live', 'retired' (removed by a swap; kept, with
-- retired_at, until the rollback retention ends). Number and slug are unique among the
-- live channels only.
--
-- tv_live_gen() answers the live generation (tv_routing scope 'cluster'; 0 without the
-- row, i.e. staging never used) and channel_programs_live is the live generation's
-- blocks: every reader of the live guide or the live join reads it.
--
-- Breaking for older nodes (maktaba_min_node_schema() = 128, at the end).
--
ALTER TABLE channel_programs ADD COLUMN IF NOT EXISTS sched_gen BIGINT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS channel_programs_channel_gen_seq_uniq
    ON channel_programs (channel_id, sched_gen, seq);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS channel_programs_channel_gen_time_idx
    ON channel_programs (channel_id, sched_gen, start_at, end_at);
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS channel_programs_channel_seq_uniq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS channel_programs_channel_time_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_schedule_state ADD COLUMN IF NOT EXISTS sched_gen BIGINT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS channel_schedule_state_gen_key
    ON channel_schedule_state (channel_id, sched_gen);
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conname = 'channel_schedule_state_gen_pkey'
                      AND conrelid = 'channel_schedule_state'::regclass) THEN
        ALTER TABLE channel_schedule_state DROP CONSTRAINT IF EXISTS channel_schedule_state_pkey;
        ALTER TABLE channel_schedule_state
            ADD CONSTRAINT channel_schedule_state_gen_pkey
            PRIMARY KEY USING INDEX channel_schedule_state_gen_key;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channels ADD COLUMN IF NOT EXISTS lifecycle TEXT NOT NULL DEFAULT 'live';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channels ADD COLUMN IF NOT EXISTS retired_at TIMESTAMPTZ;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                    WHERE conname = 'channels_lifecycle_check'
                      AND conrelid = 'channels'::regclass) THEN
        ALTER TABLE channels ADD CONSTRAINT channels_lifecycle_check
            CHECK (lifecycle IN ('staged', 'live', 'retired')) NOT VALID;
        ALTER TABLE channels VALIDATE CONSTRAINT channels_lifecycle_check;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS channels_scope_number_live_uniq
    ON channels (COALESCE(library_id, '00000000-0000-0000-0000-000000000000'::uuid), number)
    WHERE lifecycle = 'live';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS channels_scope_slug_live_uniq
    ON channels (COALESCE(library_id, '00000000-0000-0000-0000-000000000000'::uuid), slug)
    WHERE lifecycle = 'live';
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS channels_scope_number_uniq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS channels_scope_slug_uniq;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION tv_live_gen() RETURNS BIGINT
    LANGUAGE sql STABLE AS
$$ SELECT COALESCE((SELECT live_gen FROM tv_routing WHERE scope = 'cluster'), 0) $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE VIEW channel_programs_live AS
    SELECT * FROM channel_programs WHERE sched_gen = tv_live_gen();
-- +goose StatementEnd

-- +goose StatementBegin
-- Breaking for older nodes: their scheduler upserts channel_schedule_state ON CONFLICT
-- (channel_id), which no longer names a unique key, and their readers do not filter the
-- generation. Nodes below 0128 pause until updated (migrations README rule 9).
CREATE OR REPLACE FUNCTION maktaba_min_node_schema() RETURNS integer
    LANGUAGE sql IMMUTABLE AS $$ SELECT 128 $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP FUNCTION IF EXISTS maktaba_min_node_schema();
-- +goose StatementEnd

-- +goose StatementBegin
DROP VIEW IF EXISTS channel_programs_live;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS tv_live_gen();
-- +goose StatementEnd

-- +goose StatementBegin
-- only generation 0 can go back to the old shape
DELETE FROM channel_programs WHERE sched_gen <> 0;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM channel_schedule_state WHERE sched_gen <> 0;
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM channels WHERE lifecycle <> 'live';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS channels_scope_number_uniq
    ON channels (COALESCE(library_id, '00000000-0000-0000-0000-000000000000'::uuid), number);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS channels_scope_slug_uniq
    ON channels (COALESCE(library_id, '00000000-0000-0000-0000-000000000000'::uuid), slug);
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS channels_scope_number_live_uniq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS channels_scope_slug_live_uniq;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_lifecycle_check;
ALTER TABLE channels DROP COLUMN IF EXISTS retired_at;
ALTER TABLE channels DROP COLUMN IF EXISTS lifecycle;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_schedule_state DROP CONSTRAINT IF EXISTS channel_schedule_state_gen_pkey;
ALTER TABLE channel_schedule_state DROP COLUMN IF EXISTS sched_gen;
ALTER TABLE channel_schedule_state ADD PRIMARY KEY (channel_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS channel_programs_channel_seq_uniq
    ON channel_programs (channel_id, seq);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS channel_programs_channel_time_idx
    ON channel_programs (channel_id, start_at, end_at);
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS channel_programs_channel_gen_seq_uniq;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX CONCURRENTLY IF EXISTS channel_programs_channel_gen_time_idx;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_programs DROP COLUMN IF EXISTS sched_gen;
-- +goose StatementEnd
