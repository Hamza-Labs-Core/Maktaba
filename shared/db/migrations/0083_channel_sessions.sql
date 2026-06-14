-- +goose Up
-- +goose StatementBegin
--
-- Slot 0083 (Epic 27 / Story 27.3) — channel sessions + runtime registry.
--
-- A live channel is a long-lived virtual streaming session whose input
-- is a schedule, not one file. We reuse `streaming_sessions` (the slot
-- 0039 lifecycle + idle reaper) by:
--   * adding a nullable `channel_id` so a session can point at a channel
--     instead of a single video, and
--   * widening the `mode` CHECK to admit 'channel'. The slot-0039 CHECK
--     is unnamed, so Postgres auto-named it `streaming_sessions_mode_check`;
--     we drop and re-add it widened.
--
ALTER TABLE streaming_sessions ADD COLUMN IF NOT EXISTS channel_id UUID
    REFERENCES channels(id) ON DELETE CASCADE;

ALTER TABLE streaming_sessions DROP CONSTRAINT IF EXISTS streaming_sessions_mode_check;
ALTER TABLE streaming_sessions ADD CONSTRAINT streaming_sessions_mode_check
    CHECK (mode IN ('direct','remux','transcode','channel'));
-- +goose StatementEnd

-- +goose StatementBegin
-- One row per channel that is (or recently was) live. `state` drives the
-- lazy lifecycle: idle → warming → live → draining. `viewer_count` and
-- `last_segment_at` feed the reaper's grace-window teardown. `host` pins
-- the channel to the streaming replica that owns its FFmpeg (§4.2).
CREATE TABLE IF NOT EXISTS channel_runtime (
    channel_id      UUID        PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    host            TEXT        NOT NULL,
    pid             INTEGER,
    state           TEXT        NOT NULL DEFAULT 'idle'
                                CHECK (state IN ('idle','warming','live','draining')),
    viewer_count    INTEGER     NOT NULL DEFAULT 0,
    started_at      TIMESTAMPTZ,
    last_segment_at TIMESTAMPTZ
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS channel_runtime_state_idx ON channel_runtime (state, last_segment_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS channel_runtime_state_idx;
DROP TABLE IF EXISTS channel_runtime;
ALTER TABLE streaming_sessions DROP CONSTRAINT IF EXISTS streaming_sessions_mode_check;
ALTER TABLE streaming_sessions ADD CONSTRAINT streaming_sessions_mode_check
    CHECK (mode IN ('direct','remux','transcode'));
ALTER TABLE streaming_sessions DROP COLUMN IF EXISTS channel_id;
-- +goose StatementEnd
