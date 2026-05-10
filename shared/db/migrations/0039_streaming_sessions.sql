-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0039 (Epic 7 / Story 7.10) — streaming session lifecycle row.
--
-- One row per `POST /api/stream/sessions`. The row outlives the session
-- (closed_at marks the end); kept indefinitely for the watch-history
-- surface, though playback resume reads ``playback_state`` (slot 0038).
--
CREATE TABLE IF NOT EXISTS streaming_sessions (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id        UUID         NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    client_profile  TEXT,
    mode            TEXT         NOT NULL DEFAULT 'transcode'
                    CHECK (mode IN ('direct','remux','transcode')),
    audio_track_id  INTEGER,
    subtitle_lang   TEXT,
    start_sec       REAL         NOT NULL DEFAULT 0,
    max_bitrate_kbps INTEGER,
    burn_subs       BOOLEAN      NOT NULL DEFAULT false,
    metadata        JSONB        NOT NULL DEFAULT '{}'::jsonb,
    opened_at       TIMESTAMPTZ  NOT NULL DEFAULT now(),
    closed_at       TIMESTAMPTZ,
    last_seen_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS streaming_sessions_user_video_idx
    ON streaming_sessions (user_id, video_id, opened_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS streaming_sessions_open_idx
    ON streaming_sessions (opened_at DESC) WHERE closed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS streaming_sessions_open_idx;
DROP INDEX IF EXISTS streaming_sessions_user_video_idx;
DROP TABLE IF EXISTS streaming_sessions;
-- +goose StatementEnd
