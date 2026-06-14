-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0083 (Epic 27 / Story 27.3).
--
-- SQLite cannot ALTER a CHECK constraint in place, so widening `mode` to
-- admit 'channel' requires the canonical 12-step table rebuild: rename
-- the old table aside, recreate it with the new CHECK + the `channel_id`
-- column, copy the rows, drop the old table, and rebuild its indexes.
-- The column list mirrors the slot-0039 SQLite definition exactly.
--
ALTER TABLE streaming_sessions RENAME TO streaming_sessions_old_0083;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS streaming_sessions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id        TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    client_profile  TEXT,
    mode            TEXT NOT NULL DEFAULT 'transcode'
                    CHECK (mode IN ('direct','remux','transcode','channel')),
    audio_track_id  INTEGER,
    subtitle_lang   TEXT,
    start_sec       REAL NOT NULL DEFAULT 0,
    max_bitrate_kbps INTEGER,
    burn_subs       INTEGER NOT NULL DEFAULT 0,
    metadata        TEXT NOT NULL DEFAULT '{}',
    opened_at       TEXT NOT NULL DEFAULT (datetime('now')),
    closed_at       TEXT,
    last_seen_at    TEXT NOT NULL DEFAULT (datetime('now')),
    channel_id      TEXT REFERENCES channels(id) ON DELETE CASCADE
);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO streaming_sessions
       (id, user_id, video_id, client_profile, mode, audio_track_id,
        subtitle_lang, start_sec, max_bitrate_kbps, burn_subs, metadata,
        opened_at, closed_at, last_seen_at)
SELECT id, user_id, video_id, client_profile, mode, audio_track_id,
       subtitle_lang, start_sec, max_bitrate_kbps, burn_subs, metadata,
       opened_at, closed_at, last_seen_at
  FROM streaming_sessions_old_0083;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS streaming_sessions_old_0083;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS streaming_sessions_user_video_idx
    ON streaming_sessions (user_id, video_id, opened_at DESC);
CREATE INDEX IF NOT EXISTS streaming_sessions_open_idx
    ON streaming_sessions (opened_at DESC) WHERE closed_at IS NULL;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_runtime (
    channel_id      TEXT    PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    host            TEXT    NOT NULL,
    pid             INTEGER,
    state           TEXT    NOT NULL DEFAULT 'idle'
                            CHECK (state IN ('idle','warming','live','draining')),
    viewer_count    INTEGER NOT NULL DEFAULT 0,
    started_at      TEXT,
    last_segment_at TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS channel_runtime_state_idx ON channel_runtime (state, last_segment_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS channel_runtime_state_idx;
DROP TABLE IF EXISTS channel_runtime;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE streaming_sessions RENAME TO streaming_sessions_old_0083;
-- +goose StatementEnd

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
INSERT INTO streaming_sessions
       (id, user_id, video_id, client_profile, mode, audio_track_id,
        subtitle_lang, start_sec, max_bitrate_kbps, burn_subs, metadata,
        opened_at, closed_at, last_seen_at)
SELECT id, user_id, video_id, client_profile, mode, audio_track_id,
       subtitle_lang, start_sec, max_bitrate_kbps, burn_subs, metadata,
       opened_at, closed_at, last_seen_at
  FROM streaming_sessions_old_0083;
DROP TABLE IF EXISTS streaming_sessions_old_0083;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS streaming_sessions_user_video_idx
    ON streaming_sessions (user_id, video_id, opened_at DESC);
CREATE INDEX IF NOT EXISTS streaming_sessions_open_idx
    ON streaming_sessions (opened_at DESC) WHERE closed_at IS NULL;
-- +goose StatementEnd
