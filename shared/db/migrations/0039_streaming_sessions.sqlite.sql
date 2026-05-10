-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS streaming_sessions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id        TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    client_profile  TEXT,
    mode            TEXT NOT NULL DEFAULT 'transcode'
                    CHECK (mode IN ('direct','remux','transcode')),
    audio_track_id  INTEGER,
    subtitle_lang   TEXT,
    start_sec       REAL NOT NULL DEFAULT 0,
    max_bitrate_kbps INTEGER,
    burn_subs       INTEGER NOT NULL DEFAULT 0,
    metadata        TEXT NOT NULL DEFAULT '{}',
    opened_at       TEXT NOT NULL DEFAULT (datetime('now')),
    closed_at       TEXT,
    last_seen_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS streaming_sessions_user_video_idx
    ON streaming_sessions (user_id, video_id, opened_at DESC);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS streaming_sessions_open_idx
    ON streaming_sessions (opened_at DESC) WHERE closed_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS streaming_sessions_open_idx;
DROP INDEX IF EXISTS streaming_sessions_user_video_idx;
DROP TABLE IF EXISTS streaming_sessions;
-- +goose StatementEnd
