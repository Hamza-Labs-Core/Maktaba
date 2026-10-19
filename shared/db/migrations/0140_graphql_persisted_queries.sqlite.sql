-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0140: GraphQL persisted queries (Story
-- 7.17 AC-5), keyed by the operation's lowercase hex sha256.
--
CREATE TABLE IF NOT EXISTS graphql_persisted_queries (
    sha256       TEXT PRIMARY KEY,
    query        TEXT NOT NULL,
    created_by   TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_used_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS graphql_persisted_queries;
-- +goose StatementEnd
