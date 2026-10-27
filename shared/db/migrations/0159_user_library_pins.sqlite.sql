-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0159 (Epic 45, story 45.4): each user's pinned libraries,
-- in their order, and the indexes the browsing rows read. UUID becomes TEXT, TIMESTAMPTZ
-- TEXT.
--
CREATE TABLE IF NOT EXISTS user_library_pins (
    user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id TEXT    NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (user_id, library_id)
);
CREATE INDEX IF NOT EXISTS videos_created_at_idx
    ON videos (created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS videos_library_created_idx
    ON videos (library_id, created_at DESC);
CREATE INDEX IF NOT EXISTS videos_library_mtime_idx
    ON videos (library_id, mtime DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_library_mtime_idx;
DROP INDEX IF EXISTS videos_library_created_idx;
DROP INDEX IF EXISTS videos_created_at_idx;
DROP TABLE IF EXISTS user_library_pins;
-- +goose StatementEnd
