-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0092 (plan 06-11 section 10.6): UUID and
-- TIMESTAMPTZ become TEXT, JSONB becomes TEXT, BIGINT becomes INTEGER.
--
CREATE TABLE IF NOT EXISTS plex_items (
    server           TEXT    NOT NULL,
    rating_key       TEXT    NOT NULL,
    section_key      TEXT    NOT NULL,
    section_title    TEXT    NOT NULL,
    library_id       TEXT    REFERENCES libraries(id) ON DELETE SET NULL,
    video_id         TEXT    REFERENCES videos(id) ON DELETE SET NULL,
    file             TEXT,
    duration_ms      INTEGER,
    plex_updated_at  INTEGER,
    detail           TEXT    NOT NULL,
    seen_at          TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (server, rating_key)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS plex_items_section_idx ON plex_items (server, section_key);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS plex_items_video_idx ON plex_items (video_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plex_items;
-- +goose StatementEnd
