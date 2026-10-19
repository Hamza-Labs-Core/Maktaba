-- +goose Up
-- +goose StatementBegin
--
-- Slot 0128 (Epic 33, story 33.3), SQLite twin: schedule generations. SQLite has no
-- functions: tv_live_gen() is the subquery the view uses (the SQLite repos inline it).
-- channel_schedule_state is rebuilt for its (channel, generation) primary key.
--
ALTER TABLE channel_programs ADD COLUMN IF NOT EXISTS sched_gen INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS channel_programs_channel_seq_uniq;
DROP INDEX IF EXISTS channel_programs_channel_time_idx;
CREATE UNIQUE INDEX IF NOT EXISTS channel_programs_channel_gen_seq_uniq
    ON channel_programs (channel_id, sched_gen, seq);
CREATE INDEX IF NOT EXISTS channel_programs_channel_gen_time_idx
    ON channel_programs (channel_id, sched_gen, start_at, end_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_schedule_state_0128 (
    channel_id        TEXT    NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    sched_gen         INTEGER NOT NULL DEFAULT 0,
    anchor_at         TEXT,
    horizon_until     TEXT,
    last_generated_at TEXT,
    generator_version INTEGER NOT NULL DEFAULT 1,
    cursor            TEXT    NOT NULL DEFAULT '{}',
    stale             INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (channel_id, sched_gen)
);
INSERT INTO channel_schedule_state_0128
       (channel_id, sched_gen, anchor_at, horizon_until, last_generated_at,
        generator_version, cursor, stale)
SELECT channel_id, 0, anchor_at, horizon_until, last_generated_at,
       generator_version, cursor, stale
  FROM channel_schedule_state;
DROP TABLE IF EXISTS channel_schedule_state;
ALTER TABLE channel_schedule_state_0128 RENAME TO channel_schedule_state;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channels ADD COLUMN IF NOT EXISTS lifecycle TEXT NOT NULL DEFAULT 'live'
    CHECK (lifecycle IN ('staged', 'live', 'retired'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channels ADD COLUMN IF NOT EXISTS retired_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS channels_scope_number_uniq;
DROP INDEX IF EXISTS channels_scope_slug_uniq;
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_number_live_uniq
    ON channels (COALESCE(library_id, ''), number) WHERE lifecycle = 'live';
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_slug_live_uniq
    ON channels (COALESCE(library_id, ''), slug) WHERE lifecycle = 'live';
-- +goose StatementEnd

-- +goose StatementBegin
CREATE VIEW IF NOT EXISTS channel_programs_live AS
    SELECT * FROM channel_programs
     WHERE sched_gen = COALESCE((SELECT live_gen FROM tv_routing WHERE scope = 'cluster'), 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS channel_programs_live;
DELETE FROM channel_programs WHERE sched_gen <> 0;
DELETE FROM channels WHERE lifecycle <> 'live';
DROP INDEX IF EXISTS channels_scope_number_live_uniq;
DROP INDEX IF EXISTS channels_scope_slug_live_uniq;
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_number_uniq
    ON channels (COALESCE(library_id, ''), number);
CREATE UNIQUE INDEX IF NOT EXISTS channels_scope_slug_uniq
    ON channels (COALESCE(library_id, ''), slug);
ALTER TABLE channels DROP COLUMN retired_at;
ALTER TABLE channels DROP COLUMN lifecycle;
CREATE TABLE IF NOT EXISTS channel_schedule_state_0128 (
    channel_id        TEXT    PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    anchor_at         TEXT,
    horizon_until     TEXT,
    last_generated_at TEXT,
    generator_version INTEGER NOT NULL DEFAULT 1,
    cursor            TEXT    NOT NULL DEFAULT '{}',
    stale             INTEGER NOT NULL DEFAULT 0
);
INSERT INTO channel_schedule_state_0128
       (channel_id, anchor_at, horizon_until, last_generated_at, generator_version, cursor, stale)
SELECT channel_id, anchor_at, horizon_until, last_generated_at, generator_version, cursor, stale
  FROM channel_schedule_state WHERE sched_gen = 0;
DROP TABLE IF EXISTS channel_schedule_state;
ALTER TABLE channel_schedule_state_0128 RENAME TO channel_schedule_state;
DROP INDEX IF EXISTS channel_programs_channel_gen_seq_uniq;
DROP INDEX IF EXISTS channel_programs_channel_gen_time_idx;
CREATE INDEX IF NOT EXISTS channel_programs_channel_time_idx
    ON channel_programs (channel_id, start_at, end_at);
CREATE UNIQUE INDEX IF NOT EXISTS channel_programs_channel_seq_uniq
    ON channel_programs (channel_id, seq);
ALTER TABLE channel_programs DROP COLUMN sched_gen;
-- +goose StatementEnd
