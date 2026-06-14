-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0082 (Epic 27 / Story 27.2).
--
-- UUID→TEXT, JSONB→TEXT, TIMESTAMPTZ→TEXT (ISO-8601), BOOLEAN→INTEGER.
-- The contiguity invariant and absolute-time anchoring are enforced by
-- the scheduler in app code; the table only stores the result.
--
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
CREATE INDEX IF NOT EXISTS channel_programs_channel_time_idx
    ON channel_programs (channel_id, start_at, end_at);
CREATE UNIQUE INDEX IF NOT EXISTS channel_programs_channel_seq_uniq
    ON channel_programs (channel_id, seq);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_schedule_state (
    channel_id        TEXT    PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    anchor_at         TEXT,
    horizon_until     TEXT,
    last_generated_at TEXT,
    generator_version INTEGER NOT NULL DEFAULT 1,
    cursor            TEXT    NOT NULL DEFAULT '{}',
    stale             INTEGER NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_schedule_state;
DROP INDEX IF EXISTS channel_programs_channel_seq_uniq;
DROP INDEX IF EXISTS channel_programs_channel_time_idx;
DROP TABLE IF EXISTS channel_programs;
-- +goose StatementEnd
