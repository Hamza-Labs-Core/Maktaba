-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0090 (plan 06-11 section 4.4).
--
-- Type swaps: UUID, DATE and TIMESTAMPTZ become TEXT, BOOLEAN becomes INTEGER,
-- JSONB becomes TEXT, BIGSERIAL becomes INTEGER PRIMARY KEY AUTOINCREMENT.
-- series_seasons is created by the slot 0088 twin.
--
CREATE TABLE IF NOT EXISTS video_signals (
    video_id        TEXT PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    thumb_dhash     TEXT,
    thumb_version   INTEGER,
    audio_head      TEXT,
    audio_tail      TEXT,
    frame_hashes    TEXT,
    fp_version      INTEGER,
    fp_partial      INTEGER NOT NULL DEFAULT 0,
    style_metrics   TEXT,
    style_version   INTEGER,
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_channels (
    channel_id  TEXT PRIMARY KEY,
    library_id  TEXT REFERENCES libraries(id) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    title       TEXT,
    status      TEXT CHECK (status IN ('ok','gone','error','bot_check')),
    error       TEXT,
    fetched_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_videos (
    youtube_id    TEXT PRIMARY KEY,
    channel_id    TEXT,
    title         TEXT NOT NULL,
    upload_date   TEXT,
    date_quality  TEXT CHECK (date_quality IN ('exact','approximate')),
    duration_s    REAL,
    tab           TEXT,
    description   TEXT,
    source        TEXT CHECK (source IN ('info_json','youtube')),
    fetched_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_playlists (
    playlist_id  TEXT PRIMARY KEY,
    channel_id   TEXT,
    title        TEXT NOT NULL,
    item_count   INTEGER,
    is_real      INTEGER NOT NULL,
    source       TEXT,
    fetched_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_playlist_items (
    playlist_id  TEXT NOT NULL,
    youtube_id   TEXT NOT NULL,
    position     INTEGER,
    PRIMARY KEY (playlist_id, youtube_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS season_tags (
    season_id  TEXT NOT NULL REFERENCES series_seasons(id) ON DELETE CASCADE,
    tag        TEXT NOT NULL,
    score      REAL NOT NULL,
    evidence   TEXT,
    PRIMARY KEY (season_id, tag)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Named video_topic_tags, not video_tags: slot 0034 already owns video_tags.
CREATE TABLE IF NOT EXISTS video_topic_tags (
    video_id  TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    tag       TEXT NOT NULL,
    score     REAL NOT NULL,
    PRIMARY KEY (video_id, tag)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS season_style (
    season_id       TEXT PRIMARY KEY REFERENCES series_seasons(id) ON DELETE CASCADE,
    style           TEXT CHECK (style IN ('static','talk','produced','cinematic')),
    tier            INTEGER,
    confidence      REAL,
    reasons         TEXT,
    sampled         INTEGER,
    style_version   INTEGER,
    override_style  TEXT CHECK (override_style IN ('static','talk','produced','cinematic')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS external_links (
    target           TEXT NOT NULL,
    subject_kind     TEXT NOT NULL,
    subject_id       TEXT NOT NULL,
    external_id      TEXT NOT NULL,
    title            TEXT NOT NULL,
    content_hash     TEXT NOT NULL,
    created_by_tool  INTEGER NOT NULL,
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (target, subject_kind, subject_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_plans (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    planner_version  INTEGER,
    input_hash       TEXT NOT NULL,
    plan             TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS youtube_playlist_items_video_idx
    ON youtube_playlist_items (youtube_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS youtube_videos_channel_idx ON youtube_videos (channel_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS youtube_channels_library_idx ON youtube_channels (library_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS season_tags_tag_idx ON season_tags (tag, score);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS video_topic_tags_tag_idx ON video_topic_tags (tag, score);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS channel_plans_created_idx ON channel_plans (created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS channel_plans;
DROP TABLE IF EXISTS external_links;
DROP TABLE IF EXISTS season_style;
DROP TABLE IF EXISTS video_topic_tags;
DROP TABLE IF EXISTS season_tags;
DROP TABLE IF EXISTS youtube_playlist_items;
DROP TABLE IF EXISTS youtube_playlists;
DROP TABLE IF EXISTS youtube_videos;
DROP TABLE IF EXISTS youtube_channels;
DROP TABLE IF EXISTS video_signals;
-- +goose StatementEnd
