-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0220 (Epic 57, story 57.01): the age tiers' columns and
-- the recompute's queue. The triggers that put jobs in the queue are Postgres only: the
-- recompute that works it is the API's, which runs on Postgres, and a queue nothing works
-- would only grow.
--
ALTER TABLE videos ADD COLUMN IF NOT EXISTS age_tier INTEGER
    CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS age_tier_source TEXT
    CHECK (age_tier_source IS NULL OR age_tier_source IN ('admin_video', 'admin_container', 'plex',
           'guide', 'youtube', 'plugin', 'library_default'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE videos ADD COLUMN IF NOT EXISTS age_tier_admin INTEGER
    CHECK (age_tier_admin IS NULL OR age_tier_admin IN (0, 9, 13, 16, 18));
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS videos_library_age_tier_idx ON videos (library_id, age_tier);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE libraries ADD COLUMN IF NOT EXISTS age_tier INTEGER
    CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE series ADD COLUMN IF NOT EXISTS age_tier INTEGER
    CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS age_tier INTEGER
    CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channels ADD COLUMN IF NOT EXISTS age_tier INTEGER
    CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_sources ADD COLUMN IF NOT EXISTS age_tier INTEGER
    CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE channel_programs ADD COLUMN IF NOT EXISTS age_tier INTEGER
    CHECK (age_tier IS NULL OR age_tier IN (0, 9, 13, 16, 18));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE youtube_videos ADD COLUMN IF NOT EXISTS age_limit INTEGER;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS parental_recompute_jobs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT    NOT NULL
               CHECK (kind IN ('all', 'library', 'series', 'collection', 'youtube_source',
                               'youtube_video', 'video')),
    subject    TEXT    NOT NULL DEFAULT '',
    cursor_at  TEXT,
    cursor_id  TEXT,
    batches    INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down

-- +goose StatementBegin
SELECT 1;
-- +goose StatementEnd
