-- +goose NO TRANSACTION
-- +goose Up
-- +goose StatementBegin
--
-- Slot 0073 (Epic 26 / Story 26.1) — `media_parsed_titles`.
--
-- A 1:1 sidecar to `videos` holding the deterministic filename-parser
-- output (Story 26.1): show name, season/episode, year, and the
-- release-quality facets recovered from a name like
-- `Show.Name.S01E02.1080p.x265-GROUP.mkv`. Written by the `classify`
-- pipeline stage (Story 26.7); read by series detection (26.3) and the
-- enrichment matcher (26.5). `parser_version` lets a parser bump trigger
-- a targeted re-classify (Story 26.7) without re-running earlier stages.
--
CREATE TABLE IF NOT EXISTS media_parsed_titles (
    video_id        UUID         PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    show_name       TEXT,
    season          INTEGER,
    episode         INTEGER,
    absolute_number INTEGER,
    year            INTEGER,
    resolution      TEXT,
    codec           TEXT,
    release_group   TEXT,
    edition         TEXT,
    confidence      DOUBLE PRECISION NOT NULL DEFAULT 0,
    parser_version  TEXT         NOT NULL DEFAULT 'v1',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Series detection groups episodes by normalized show name; the index
-- keeps that grouping query cheap.
CREATE INDEX CONCURRENTLY IF NOT EXISTS media_parsed_titles_show_idx
    ON media_parsed_titles (show_name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS media_parsed_titles_show_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS media_parsed_titles;
-- +goose StatementEnd
