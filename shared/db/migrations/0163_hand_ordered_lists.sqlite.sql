-- +goose Up
-- +goose StatementBegin
--
-- SQLite parity sibling for slot 0163: a playlist's and a manual collection's
-- hand_ordered (0 or 1), and the lists reordered before it.
--
ALTER TABLE user_playlists ADD COLUMN IF NOT EXISTS hand_ordered INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER TABLE collections ADD COLUMN IF NOT EXISTS hand_ordered INTEGER NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE user_playlists SET hand_ordered = 1
 WHERE hand_ordered = 0 AND EXISTS (
    SELECT 1 FROM user_playlist_items a
      JOIN user_playlist_items b ON b.playlist_id = a.playlist_id
     WHERE a.playlist_id = user_playlists.id AND a.position < b.position AND a.added_at > b.added_at);
-- +goose StatementEnd

-- +goose StatementBegin
UPDATE collections SET hand_ordered = 1
 WHERE hand_ordered = 0 AND is_smart = 0 AND EXISTS (
    SELECT 1 FROM collection_items a
      JOIN collection_items b ON b.collection_id = a.collection_id
     WHERE a.collection_id = collections.id AND a.position < b.position AND a.added_at > b.added_at);
-- +goose StatementEnd
