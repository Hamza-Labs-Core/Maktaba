-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0109 (Epic 27, dizqueTV parity).
--
-- SQLite cannot ALTER a CHECK constraint in place, so channel_programs is rebuilt
-- (rename aside, recreate with 'live' in the kind CHECK and the new source_url column,
-- copy, drop the old table, rebuild its indexes). The column list mirrors the slot-0082
-- SQLite definition. filler_pools only gains a column.
--
ALTER TABLE channel_programs RENAME TO channel_programs_old_0109;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_programs (
    id              TEXT    PRIMARY KEY,
    channel_id      TEXT    NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    kind            TEXT    NOT NULL DEFAULT 'program'
                            CHECK (kind IN ('program','filler','bumper','slate','live')),
    video_id        TEXT    REFERENCES videos(id) ON DELETE SET NULL,
    filler_item_id  TEXT,
    start_at        TEXT    NOT NULL,
    end_at          TEXT    NOT NULL,
    source_offset   INTEGER NOT NULL DEFAULT 0,
    source_duration INTEGER NOT NULL,
    title_snapshot  TEXT    NOT NULL DEFAULT '{}',
    source_url      TEXT,
    CHECK (end_at > start_at)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO channel_programs
       (id, channel_id, seq, kind, video_id, filler_item_id, start_at, end_at,
        source_offset, source_duration, title_snapshot)
SELECT id, channel_id, seq, kind, video_id, filler_item_id, start_at, end_at,
       source_offset, source_duration, title_snapshot
  FROM channel_programs_old_0109;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS channel_programs_old_0109;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS channel_programs_channel_time_idx
    ON channel_programs (channel_id, start_at, end_at);
CREATE UNIQUE INDEX IF NOT EXISTS channel_programs_channel_seq_uniq
    ON channel_programs (channel_id, seq);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE filler_pools ADD COLUMN IF NOT EXISTS external_key TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS filler_pools_external_key_uniq
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
ALTER TABLE channel_programs RENAME TO channel_programs_new_0109;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_programs (
    id              TEXT    PRIMARY KEY,
    channel_id      TEXT    NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    kind            TEXT    NOT NULL DEFAULT 'program'
                            CHECK (kind IN ('program','filler','bumper','slate')),
    video_id        TEXT    REFERENCES videos(id) ON DELETE SET NULL,
    filler_item_id  TEXT,
    start_at        TEXT    NOT NULL,
    end_at          TEXT    NOT NULL,
    source_offset   INTEGER NOT NULL DEFAULT 0,
    source_duration INTEGER NOT NULL,
    title_snapshot  TEXT    NOT NULL DEFAULT '{}',
    CHECK (end_at > start_at)
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO channel_programs
       (id, channel_id, seq, kind, video_id, filler_item_id, start_at, end_at,
        source_offset, source_duration, title_snapshot)
SELECT id, channel_id, seq, kind, video_id, filler_item_id, start_at, end_at,
       source_offset, source_duration, title_snapshot
  FROM channel_programs_new_0109 WHERE kind <> 'live';
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS channel_programs_new_0109;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS channel_programs_channel_time_idx
    ON channel_programs (channel_id, start_at, end_at);
CREATE UNIQUE INDEX IF NOT EXISTS channel_programs_channel_seq_uniq
    ON channel_programs (channel_id, seq);
-- +goose StatementEnd
