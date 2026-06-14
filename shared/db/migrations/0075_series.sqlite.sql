-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0075 (Story 26.3).
CREATE TABLE IF NOT EXISTS series (
    id            TEXT    PRIMARY KEY,
    library_id    TEXT    REFERENCES libraries(id) ON DELETE SET NULL,
    name          TEXT    NOT NULL,
    name_override TEXT,
    poster_path   TEXT,
    year          INTEGER,
    numbering     TEXT    NOT NULL DEFAULT 'season'
                          CHECK (numbering IN ('season','absolute')),
    metadata      TEXT    NOT NULL DEFAULT '{}',
    created_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS series_episodes (
    series_id        TEXT    NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    video_id         TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    season           INTEGER,
    episode          INTEGER,
    absolute_number  INTEGER,
    season_override  INTEGER,
    episode_override INTEGER,
    created_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (series_id, video_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE UNIQUE INDEX IF NOT EXISTS series_episodes_video_idx
    ON series_episodes (video_id);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS series_library_idx
    ON series (library_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS series_library_idx;
DROP INDEX IF EXISTS series_episodes_video_idx;
DROP TABLE IF EXISTS series_episodes;
DROP TABLE IF EXISTS series;
-- +goose StatementEnd
