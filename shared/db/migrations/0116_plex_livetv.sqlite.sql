-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0116 (Plex Live TV state): TIMESTAMPTZ and JSONB become TEXT.
--
CREATE TABLE IF NOT EXISTS plex_livetv_state (
    id                     INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    register_requested_at  TEXT,
    requested_base_url     TEXT,
    status                 TEXT    NOT NULL DEFAULT '{}',
    checked_at             TEXT,
    guide_reloaded_at      TEXT,
    guide_version          TEXT,
    channelmap_at          TEXT,
    channelmap_hash        TEXT,
    last_error             TEXT,
    last_error_at          TEXT,
    updated_at             TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS plex_livetv_state;
-- +goose StatementEnd
