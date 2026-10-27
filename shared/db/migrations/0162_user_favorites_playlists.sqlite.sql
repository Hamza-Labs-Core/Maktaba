-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0162 (Epic 45 Phase 2): each user's favorites and
-- playlists, and the index the grid's title sort reads. UUID becomes TEXT, TIMESTAMPTZ TEXT.
--
CREATE TABLE IF NOT EXISTS user_favorites (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    video_id   TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (user_id, video_id)
);
CREATE INDEX IF NOT EXISTS user_favorites_user_created_idx
    ON user_favorites (user_id, created_at DESC);
CREATE TABLE IF NOT EXISTS user_playlists (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS user_playlists_user_idx
    ON user_playlists (user_id, updated_at DESC);
CREATE TABLE IF NOT EXISTS user_playlist_items (
    playlist_id TEXT    NOT NULL REFERENCES user_playlists(id) ON DELETE CASCADE,
    video_id    TEXT    NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    added_at    TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (playlist_id, video_id)
);
CREATE INDEX IF NOT EXISTS user_playlist_items_order_idx
    ON user_playlist_items (playlist_id, position);
CREATE INDEX IF NOT EXISTS videos_library_title_idx
    ON videos (library_id, (COALESCE(title, '')), id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_library_title_idx;
DROP TABLE IF EXISTS user_playlist_items;
DROP TABLE IF EXISTS user_playlists;
DROP TABLE IF EXISTS user_favorites;
-- +goose StatementEnd
