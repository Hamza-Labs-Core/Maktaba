-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS playback_state (
    user_id        TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id       TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position_sec   REAL NOT NULL DEFAULT 0 CHECK (position_sec >= 0),
    completed      INTEGER NOT NULL DEFAULT 0,
    audio_track_id INTEGER,
    subtitle_lang  TEXT,
    updated_at     TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS playback_state_user_updated_idx
    ON playback_state (user_id, updated_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS playback_state_user_updated_idx;
DROP TABLE IF EXISTS playback_state;
-- +goose StatementEnd
