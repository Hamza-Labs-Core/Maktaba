-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0090 (plan 06-11 section 4.4 and 10.4, workstream A) - the analysis
-- store: first-class tables for what the media-intelligence stages learn, in
-- place of videos.metadata (fingerprints of two 90 s windows run to several
-- KB per video and would bloat every library-browse SELECT).
--
-- series_seasons was created by slot 0088 (processing_jobs needs it for the
-- season_id foreign key). DDL only: importing the mediaintel prototype's cache
-- is a tool (maktaba_pipeline.tools.import_mediaintel), not SQL.
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
CREATE TABLE IF NOT EXISTS video_signals (
    video_id        UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    thumb_dhash     TEXT,
    thumb_version   INT,
    audio_head      TEXT,
    audio_tail      TEXT,
    frame_hashes    TEXT,
    fp_version      INT,
    fp_partial      BOOLEAN NOT NULL DEFAULT false,
    style_metrics   JSONB,
    style_version   INT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_channels (
    channel_id  TEXT PRIMARY KEY,
    library_id  UUID REFERENCES libraries(id) ON DELETE CASCADE,
    url         TEXT NOT NULL,
    title       TEXT,
    status      TEXT CHECK (status IN ('ok','gone','error','bot_check')),
    error       TEXT,
    fetched_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_videos (
    youtube_id    TEXT PRIMARY KEY,
    channel_id    TEXT,
    title         TEXT NOT NULL,
    upload_date   DATE,
    date_quality  TEXT CHECK (date_quality IN ('exact','approximate')),
    duration_s    REAL,
    tab           TEXT,
    description   TEXT,
    source        TEXT CHECK (source IN ('info_json','youtube')),
    fetched_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_playlists (
    playlist_id  TEXT PRIMARY KEY,
    channel_id   TEXT,
    title        TEXT NOT NULL,
    item_count   INT,
    is_real      BOOLEAN NOT NULL,
    source       TEXT,
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_playlist_items (
    playlist_id  TEXT NOT NULL,
    youtube_id   TEXT NOT NULL,
    position     INT,
    PRIMARY KEY (playlist_id, youtube_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS season_tags (
    season_id  UUID NOT NULL REFERENCES series_seasons(id) ON DELETE CASCADE,
    tag        TEXT NOT NULL,
    score      REAL NOT NULL,
    evidence   JSONB,
    PRIMARY KEY (season_id, tag)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Named video_topic_tags, not video_tags: slot 0034 already owns video_tags
-- (the user tag taxonomy, keyed by tag_id).
CREATE TABLE IF NOT EXISTS video_topic_tags (
    video_id  UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    tag       TEXT NOT NULL,
    score     REAL NOT NULL,
    PRIMARY KEY (video_id, tag)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS season_style (
    season_id       UUID PRIMARY KEY REFERENCES series_seasons(id) ON DELETE CASCADE,
    style           TEXT CHECK (style IN ('static','talk','produced','cinematic')),
    tier            SMALLINT,
    confidence      REAL,
    reasons         JSONB,
    sampled         INT,
    style_version   INT,
    override_style  TEXT CHECK (override_style IN ('static','talk','produced','cinematic')),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- target is an open vocabulary (maktaba_channel for core, plex_collection,
-- plex_item, dizquetv_show, dizquetv_channel for extensions).
-- subject_id is TEXT: seasons use their UUID, channels use the plan's channel key.
CREATE TABLE IF NOT EXISTS external_links (
    target           TEXT NOT NULL,
    subject_kind     TEXT NOT NULL,
    subject_id       TEXT NOT NULL,
    external_id      TEXT NOT NULL,
    title            TEXT NOT NULL,
    content_hash     TEXT NOT NULL,
    created_by_tool  BOOLEAN NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (target, subject_kind, subject_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS channel_plans (
    id               BIGSERIAL PRIMARY KEY,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    planner_version  INT,
    input_hash       TEXT NOT NULL,
    plan             JSONB NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS youtube_playlist_items_video_idx
    ON youtube_playlist_items (youtube_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS youtube_videos_channel_idx
    ON youtube_videos (channel_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS youtube_channels_library_idx
    ON youtube_channels (library_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS season_tags_tag_idx ON season_tags (tag, score);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS video_topic_tags_tag_idx ON video_topic_tags (tag, score);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS channel_plans_created_idx ON channel_plans (created_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose NO TRANSACTION
DROP INDEX IF EXISTS channel_plans_created_idx;
DROP INDEX IF EXISTS video_topic_tags_tag_idx;
DROP INDEX IF EXISTS season_tags_tag_idx;
DROP INDEX IF EXISTS youtube_channels_library_idx;
DROP INDEX IF EXISTS youtube_videos_channel_idx;
DROP INDEX IF EXISTS youtube_playlist_items_video_idx;

-- +goose StatementBegin
DROP TABLE IF EXISTS channel_plans;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS external_links;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS season_style;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS video_topic_tags;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS season_tags;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_playlist_items;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_playlists;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_videos;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_channels;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS video_signals;
-- +goose StatementEnd
