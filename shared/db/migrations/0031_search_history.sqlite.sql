-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS search_history (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       TEXT    REFERENCES users(id) ON DELETE CASCADE,
    query         TEXT    NOT NULL,
    query_norm    TEXT    NOT NULL,
    hits          INTEGER NOT NULL DEFAULT 1,
    result_count  INTEGER,
    first_used_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    last_used_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    UNIQUE (user_id, query_norm)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS search_history_prefix_idx
    ON search_history (query_norm);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS search_history_recent_idx
    ON search_history (user_id, last_used_at DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS search_history;
-- +goose StatementEnd
