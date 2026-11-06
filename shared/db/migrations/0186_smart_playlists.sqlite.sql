-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0186 (Epic 56, story 56.09): user_playlists.kind
-- ('manual' or 'smart') and smart_query (JSON text). SQLite cannot add a table check to
-- an existing table, so the column checks below hold the same rules: kind is one of the
-- two, and smart_query is set exactly when kind is 'smart'.
--
ALTER TABLE user_playlists ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'manual'
    CHECK (kind IN ('manual', 'smart'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE user_playlists ADD COLUMN IF NOT EXISTS smart_query TEXT
    CHECK ((kind = 'smart') = (smart_query IS NOT NULL));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE user_playlists DROP COLUMN smart_query;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE user_playlists DROP COLUMN kind;
-- +goose StatementEnd
