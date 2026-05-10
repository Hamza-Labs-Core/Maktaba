-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0030.
--
CREATE TABLE IF NOT EXISTS library_acl (
    user_id     TEXT     NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id  TEXT     NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    granted_at  TEXT     NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (user_id, library_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS library_acl_library_idx
    ON library_acl (library_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS library_acl_library_idx;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS library_acl;
-- +goose StatementEnd
