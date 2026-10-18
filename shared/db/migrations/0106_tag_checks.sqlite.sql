-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0102: UUID and TIMESTAMPTZ become TEXT.
--
CREATE TABLE IF NOT EXISTS tag_checks (
    season_id     TEXT        PRIMARY KEY REFERENCES series_seasons(id) ON DELETE CASCADE,
    input_hash    TEXT        NOT NULL,
    model         TEXT        NOT NULL,
    answer        TEXT        NOT NULL,
    applied_hash  TEXT,
    checked_at    TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS clip_checks (
    video_id      TEXT        PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    input_hash    TEXT        NOT NULL,
    model         TEXT        NOT NULL,
    topics        TEXT        NOT NULL,
    applied_hash  TEXT,
    checked_at    TEXT        NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS clip_checks;
DROP TABLE IF EXISTS tag_checks;
-- +goose StatementEnd
