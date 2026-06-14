-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0073 (Story 26.1). Timestamps are ISO
-- strings; DOUBLE maps to REAL.
CREATE TABLE IF NOT EXISTS media_parsed_titles (
    video_id        TEXT    PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    show_name       TEXT,
    season          INTEGER,
    episode         INTEGER,
    absolute_number INTEGER,
    year            INTEGER,
    resolution      TEXT,
    codec           TEXT,
    release_group   TEXT,
    edition         TEXT,
    confidence      REAL    NOT NULL DEFAULT 0,
    parser_version  TEXT    NOT NULL DEFAULT 'v1',
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS media_parsed_titles_show_idx
    ON media_parsed_titles (show_name);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS media_parsed_titles_show_idx;
DROP TABLE IF EXISTS media_parsed_titles;
-- +goose StatementEnd
