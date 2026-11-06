-- +goose Up
-- +goose StatementBegin
--
-- Slot 0191 (Epic 56, story 56.09) - smart playlists.
--
-- user_playlists.kind: 'manual' (its items are user_playlist_items) or 'smart' (a saved
-- filter, evaluated when read: smart_query holds {libraries, filters, sort, order, limit},
-- the GET /api/videos keys of FilterKeys, libraries null for every library the owner can
-- read, limit 1..5000). A smart playlist holds no user_playlist_items; the API refuses item
-- writes on one (409 smart_playlist). Additive: every playlist so far is manual.
--
ALTER TABLE user_playlists ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'manual';
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE user_playlists ADD COLUMN IF NOT EXISTS smart_query JSONB;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE user_playlists DROP CONSTRAINT IF EXISTS user_playlists_kind_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE user_playlists ADD CONSTRAINT user_playlists_kind_check
    CHECK (kind IN ('manual', 'smart'));
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE user_playlists DROP CONSTRAINT IF EXISTS user_playlists_smart_query_check;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE user_playlists ADD CONSTRAINT user_playlists_smart_query_check
    CHECK ((kind = 'smart') = (smart_query IS NOT NULL));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE user_playlists DROP CONSTRAINT IF EXISTS user_playlists_smart_query_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE user_playlists DROP CONSTRAINT IF EXISTS user_playlists_kind_check;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE user_playlists DROP COLUMN IF EXISTS smart_query;
-- +goose StatementEnd
-- +goose StatementBegin
ALTER TABLE user_playlists DROP COLUMN IF EXISTS kind;
-- +goose StatementEnd
