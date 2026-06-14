-- +goose Up
-- +goose StatementBegin
-- SQLite parity sibling for slot 0080 (Story 26.8).
CREATE TABLE IF NOT EXISTS youtube_search_cache (
    query_hash TEXT    PRIMARY KEY,
    response   TEXT    NOT NULL,
    fetched_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at TEXT    NOT NULL
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS youtube_imports (
    id            TEXT PRIMARY KEY,
    video_id      TEXT NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    youtube_id    TEXT NOT NULL,
    actor_user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    imported_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (video_id, youtube_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS youtube_search_cache_expiry_idx
    ON youtube_search_cache (expires_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS youtube_search_cache_expiry_idx;
DROP TABLE IF EXISTS youtube_imports;
DROP TABLE IF EXISTS youtube_search_cache;
-- +goose StatementEnd
