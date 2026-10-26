-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0153 (Epic 34, story 34.1): the public site's channels
-- and their rights confirmation. A SQLite home is a single machine: no roles. UUID
-- becomes TEXT, TIMESTAMPTZ TEXT, BOOLEAN INTEGER. The slug's pattern is checked by the
-- API (SQLite has no regular expressions).
--
CREATE TABLE IF NOT EXISTS tv_public_channels (
    channel_id           TEXT    PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    public               INTEGER NOT NULL DEFAULT 0,
    slug                 TEXT    NOT NULL UNIQUE,
    rights_confirmed_by  TEXT,
    rights_confirmed_at  TEXT,
    rights_note          TEXT    NOT NULL DEFAULT '',
    updated_at           TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_by           TEXT
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS tv_public_channels;
-- +goose StatementEnd
