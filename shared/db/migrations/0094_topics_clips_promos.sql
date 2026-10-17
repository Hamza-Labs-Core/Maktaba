-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0094 (plan 06-11 sections 10.8 and 17) - what detection leaves over, per
-- library: topic collections (curated playlists cutting across shows, which
-- may overlap seasons), clip groups (shorts, clips and stand-alone videos, one group
-- per video, for filler and programme blocks) and promo links (a promo and the show
-- it advertises). Written by the topic_collections, clip_groups and promo_links
-- stages; read by the Plex extension's plex_groups and the channel planner.
-- Also seeds the three stages and the new detect / tag edges into job_stages.
--
-- Runs outside a transaction because the indexes are built CONCURRENTLY.
--
CREATE TABLE IF NOT EXISTS topic_collections (
    id               UUID        PRIMARY KEY,
    library_id       UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    playlist_id      TEXT        NOT NULL,
    name             TEXT        NOT NULL,
    membership_hash  TEXT        NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (library_id, playlist_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS topic_items (
    topic_id  UUID NOT NULL REFERENCES topic_collections(id) ON DELETE CASCADE,
    video_id  UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position  INT  NOT NULL,
    PRIMARY KEY (topic_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS clip_groups (
    id               UUID        PRIMARY KEY,
    library_id       UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind             TEXT        NOT NULL CHECK (kind IN ('clips','standalone')),
    basis            TEXT        NOT NULL CHECK (basis IN ('playlist','topic','rest','promo')),
    name             TEXT        NOT NULL,
    topic            TEXT,
    title            TEXT        NOT NULL,
    membership_hash  TEXT        NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS clip_group_items (
    group_id  UUID NOT NULL REFERENCES clip_groups(id) ON DELETE CASCADE,
    video_id  UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position  INT  NOT NULL,
    PRIMARY KEY (group_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS promo_links (
    video_id    UUID        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    library_id  UUID        NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    series_id   UUID        NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    season_id   UUID        REFERENCES series_seasons(id) ON DELETE SET NULL,
    season      INT,
    score       REAL        NOT NULL,
    evidence    TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS clip_group_items_video_idx ON clip_group_items (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS clip_groups_library_idx ON clip_groups (library_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS topic_items_video_idx ON topic_items (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS promo_links_library_idx ON promo_links (library_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS promo_links_series_idx ON promo_links (series_id);
-- +goose StatementEnd

-- +goose StatementBegin
INSERT INTO job_stages
    (stage, label, subject_kinds, pool, lane, lane_order, version, debounce_sec,
     debounce_max_sec, max_attempts, edges, may_request, entry_for, legacy, hidden,
     external_write, write_mode)
VALUES
    ('topic_collections', 'Topic collections', ARRAY['library']::text[], 'cpu', 'seasons', 2, 1, 30, 300, 3, '[{"to":"clip_groups","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['library']::text[], false, false, false, 'auto'),
    ('promo_links', 'Promo links', ARRAY['library']::text[], 'cpu', 'seasons', 2, 1, 30, 300, 3, '[{"to":"clip_groups","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['library']::text[], false, false, false, 'auto'),
    ('clip_groups', 'Clip groups', ARRAY['library']::text[], 'cpu', 'seasons', 3, 1, 30, 300, 3, '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb, '{}'::text[], ARRAY['library']::text[], false, false, false, 'auto')
ON CONFLICT (stage) DO NOTHING;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"review_pages","map":"same","when":"output_changed","legacy":false},{"to":"topic_collections","map":"same","when":"output_changed","legacy":false},{"to":"promo_links","map":"same","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"clip_groups","map":"library_of","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'tag';
-- +goose StatementEnd
-- +goose StatementBegin
-- the detector with title-brand and numbered shows (PR #48) is version 3
UPDATE job_stages SET version = 3 WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE job_stages SET version = 2 WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"tag","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"rate_style","map":"seasons_changed","when":"output_changed","legacy":false},{"to":"tag","map":"same","when":"output_changed","legacy":false},{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false},{"to":"review_pages","map":"same","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'detect';
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE job_stages SET edges = '[{"to":"plan_channels","map":"catalog","when":"output_changed","legacy":false}]'::jsonb WHERE stage = 'tag';
-- +goose StatementEnd

-- +goose StatementBegin
DELETE FROM job_stages WHERE stage IN ('topic_collections', 'promo_links', 'clip_groups');
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS promo_links;
DROP TABLE IF EXISTS clip_group_items;
DROP TABLE IF EXISTS clip_groups;
DROP TABLE IF EXISTS topic_items;
DROP TABLE IF EXISTS topic_collections;
-- +goose StatementEnd
