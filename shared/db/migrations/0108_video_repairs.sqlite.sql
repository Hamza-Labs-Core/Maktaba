-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0108 (plan 06-11, mediaintel parity): UUID and TIMESTAMPTZ
-- become TEXT.
--
CREATE TABLE IF NOT EXISTS video_repairs (
    video_id     TEXT    PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    library_id   TEXT    REFERENCES libraries(id) ON DELETE CASCADE,
    youtube_id   TEXT,
    state        TEXT    NOT NULL CHECK (state IN ('repaired','readable','failed','no_id')),
    message      TEXT,
    backup_path  TEXT,
    attempts     INTEGER NOT NULL DEFAULT 1,
    updated_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS video_repairs_library_idx ON video_repairs (library_id, state);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS video_repairs_library_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS video_repairs;
-- +goose StatementEnd
