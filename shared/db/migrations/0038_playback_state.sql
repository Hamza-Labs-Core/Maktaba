-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0038 (Epic 7 / Story 7.11) — per-user, per-video playback resume
-- point.
--
-- One row per (user, video). The API debounces upserts so this table is
-- not flooded — even a player that POSTs every 100 ms produces ≤1 write
-- per second per session at the persistence layer.
--
CREATE TABLE IF NOT EXISTS playback_state (
    user_id        UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id       UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position_sec   REAL         NOT NULL DEFAULT 0 CHECK (position_sec >= 0),
    completed      BOOLEAN      NOT NULL DEFAULT false,
    audio_track_id INTEGER,
    subtitle_lang  TEXT,
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Story 7.21 AC-2 explicitly requires this index for the
-- "Continue Watching" rail's ordering.
CREATE INDEX CONCURRENTLY IF NOT EXISTS playback_state_user_updated_idx
    ON playback_state (user_id, updated_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS playback_state_user_updated_idx;
DROP TABLE IF EXISTS playback_state;
-- +goose StatementEnd
