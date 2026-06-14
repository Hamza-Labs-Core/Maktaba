-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0082 (Epic 27 / Story 27.2) — the linear schedule.
--
-- The schedule is wall-clock anchored: every block carries absolute
-- `start_at`/`end_at` (UTC) and the timeline is contiguous
-- (`prev.end_at == next.start_at`). Two viewers tuning at the same
-- second compute the same seek and see the same frame; the guide and
-- "what's on now" are pure time-range queries. `source_offset`/
-- `source_duration` (ms) say which slice of the underlying media this
-- block plays — a marathon that splits a long file across boundaries,
-- or filler trimmed to a slot, both use these.
--
-- `title_snapshot` caches the display metadata at generation time (D8)
-- so a guide read never joins the whole library and stays stable if the
-- source video is later edited.
--
CREATE TABLE IF NOT EXISTS channel_programs (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id      UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    seq             BIGINT      NOT NULL,                       -- monotonic per channel
    kind            TEXT        NOT NULL DEFAULT 'program'
                                CHECK (kind IN ('program','filler','bumper','slate')),
    video_id        UUID        REFERENCES videos(id) ON DELETE SET NULL,
    filler_item_id  UUID,                                       -- → filler_items (slot 0085)
    start_at        TIMESTAMPTZ NOT NULL,
    end_at          TIMESTAMPTZ NOT NULL,
    source_offset   INTEGER     NOT NULL DEFAULT 0,             -- ms into the source
    source_duration INTEGER     NOT NULL,                       -- ms played from the source
    title_snapshot  JSONB       NOT NULL DEFAULT '{}'::jsonb,   -- D8: cached guide metadata
    CHECK (end_at > start_at)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Time-range lookups (guide + "what's on now" + live join) are the hot path.
CREATE INDEX CONCURRENTLY IF NOT EXISTS channel_programs_channel_time_idx
    ON channel_programs (channel_id, start_at, end_at);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS channel_programs_channel_seq_uniq
    ON channel_programs (channel_id, seq);
-- +goose StatementEnd

-- +goose StatementBegin
-- Per-channel generator state. `cursor` persists the shuffle bag /
-- marathon index across top-ups so a horizon extension continues the
-- sequence rather than reshuffling (D5). `stale` is flipped by a rule
-- change in 27.1 (D5) and consumed by the debounced regen.
CREATE TABLE IF NOT EXISTS channel_schedule_state (
    channel_id        UUID        PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    anchor_at         TIMESTAMPTZ,
    horizon_until     TIMESTAMPTZ,
    last_generated_at TIMESTAMPTZ,
    generator_version INTEGER     NOT NULL DEFAULT 1,
    cursor            JSONB       NOT NULL DEFAULT '{}'::jsonb,  -- D5: shuffle bag / marathon idx
    stale             BOOLEAN     NOT NULL DEFAULT false         -- set by rule change (27.1 D5)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_schedule_state;
DROP INDEX IF EXISTS channel_programs_channel_seq_uniq;
DROP INDEX IF EXISTS channel_programs_channel_time_idx;
DROP TABLE IF EXISTS channel_programs;
-- +goose StatementEnd
