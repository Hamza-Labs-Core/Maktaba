-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0228 (Epic 50, story 50.24): the takedowns a home applied.
-- Broadcasting runs only against Postgres, so the function that reads it is Postgres only;
-- the table keeps the schemas alike.
--
CREATE TABLE IF NOT EXISTS broadcast_takedowns (
    id           TEXT     PRIMARY KEY,
    notice_id    TEXT     NOT NULL DEFAULT '',
    scope        TEXT     NOT NULL CHECK (scope IN ('item', 'source')),
    video_id     TEXT     REFERENCES videos(id) ON DELETE SET NULL,
    source_kind  TEXT     NOT NULL DEFAULT '' CHECK (source_kind IN ('', 'channel', 'playlist', 'speaker', 'label')),
    source_ref   TEXT     NOT NULL DEFAULT '',
    channel_id   TEXT     REFERENCES channels(id) ON DELETE SET NULL,
    programme    TEXT     NOT NULL DEFAULT '',
    applied_at   TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    restored_at  TEXT,
    restored_by  TEXT     NOT NULL DEFAULT ''
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS broadcast_takedowns_video_idx ON broadcast_takedowns (video_id) WHERE restored_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS broadcast_takedowns_video_idx;
DROP TABLE IF EXISTS broadcast_takedowns;
-- +goose StatementEnd
