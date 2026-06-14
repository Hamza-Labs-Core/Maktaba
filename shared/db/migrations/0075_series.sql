-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0075 (Epic 26 / Story 26.3) — `series` + `series_episodes`.
--
-- A `series` groups episodes detected to belong to the same show. It is
-- conceptually cross-library: the row records a "home" `library_id` (the
-- library most of its episodes live in, nullable) but membership is by
-- `series_episodes.video_id`, and a video can live in any library. The
-- cross-library browser (Story 26.10) ACL-filters episodes by each
-- video's own library.
--
-- User overrides (rename, episode reorder) are recorded with
-- `name_override` and the per-episode `season_override`/`episode_override`
-- columns so a re-detect never clobbers a human decision (Story 26.3 AC).
-- `numbering` is 'season' (default) or 'absolute' and drives ordering +
-- missing-episode detection (Story 26.10).
--
CREATE TABLE IF NOT EXISTS series (
    id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    library_id    UUID         REFERENCES libraries(id) ON DELETE SET NULL,
    name          TEXT         NOT NULL,
    name_override TEXT,
    poster_path   TEXT,
    year          INTEGER,
    numbering     TEXT         NOT NULL DEFAULT 'season'
                               CHECK (numbering IN ('season','absolute')),
    metadata      JSONB        NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS series_episodes (
    series_id        UUID    NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    video_id         UUID    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    season           INTEGER,
    episode          INTEGER,
    absolute_number  INTEGER,
    season_override  INTEGER,
    episode_override INTEGER,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (series_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
-- A video belongs to at most one series; the browser and context card
-- both resolve a video → its series in O(1).
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS series_episodes_video_idx
    ON series_episodes (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX CONCURRENTLY IF NOT EXISTS series_library_idx
    ON series (library_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS series_library_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP INDEX IF EXISTS series_episodes_video_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS series_episodes;
DROP TABLE IF EXISTS series;
-- +goose StatementEnd
