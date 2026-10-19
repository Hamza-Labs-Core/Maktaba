-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0134 (Epic 35 story 35.9). A SQLite home cannot take
-- other machines, so no session is ever created through one; the columns and the table
-- exist so the same code runs on both.
--
CREATE TABLE IF NOT EXISTS cluster_sso_codes (
    code_hash          TEXT PRIMARY KEY,
    node_id            TEXT NOT NULL REFERENCES cluster_nodes(id) ON DELETE CASCADE,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_session_id  TEXT NOT NULL REFERENCES web_sessions(id) ON DELETE CASCADE,
    pkce_challenge     TEXT NOT NULL,
    return_path        TEXT NOT NULL,
    created_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at         TEXT NOT NULL,
    used_at            TEXT
);
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE web_sessions ADD COLUMN IF NOT EXISTS origin_node_id TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE web_sessions ADD COLUMN IF NOT EXISTS parent_session_id TEXT REFERENCES web_sessions(id) ON DELETE CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS delegation_key_id TEXT;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE cluster_nodes ADD COLUMN IF NOT EXISTS delegation_rotated_at TEXT;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS cluster_sso_codes;
-- +goose StatementEnd
