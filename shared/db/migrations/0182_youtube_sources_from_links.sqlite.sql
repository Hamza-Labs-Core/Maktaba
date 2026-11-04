-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0182: youtube_sources.derived_at (when the derivation
-- made the source from the library's linked videos), youtube_channel_libraries (every
-- library a channel's linked videos are in) and youtube_source_decisions (what the
-- derivation decided for each library, and why). No backfill: the pipeline derives.
--
ALTER TABLE youtube_sources ADD COLUMN IF NOT EXISTS derived_at TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_channel_libraries (
    channel_id     TEXT    NOT NULL,
    library_id     TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    linked_videos  INTEGER NOT NULL DEFAULT 0,
    updated_at     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (channel_id, library_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_source_decisions (
    library_id         TEXT    PRIMARY KEY REFERENCES libraries(id) ON DELETE CASCADE,
    outcome            TEXT    NOT NULL
                       CHECK (outcome IN ('playlist', 'channel', 'clash', 'mixed', 'none')),
    kind               TEXT    CHECK (kind IN ('channel', 'playlist')),
    external_ids       TEXT    NOT NULL DEFAULT '[]',
    created            TEXT    NOT NULL DEFAULT '[]',
    clash_external_id  TEXT,
    clash_library_id   TEXT    REFERENCES libraries(id) ON DELETE SET NULL,
    linked_videos      INTEGER NOT NULL DEFAULT 0,
    matched_videos     INTEGER NOT NULL DEFAULT 0,
    reason             TEXT    NOT NULL,
    seen               TEXT    NOT NULL DEFAULT '[]',
    decided_at         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS youtube_channel_libraries_library_idx
    ON youtube_channel_libraries (library_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_source_decisions;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS youtube_channel_libraries;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE youtube_sources DROP COLUMN derived_at;
-- +goose StatementEnd
