-- +goose Up
-- +goose StatementBegin
--
-- Slot 0163: every list of videos opens newest-added first unless the person
-- ordered it by hand. A playlist (slot 0162) or a manual collection (slot 0033)
-- answers its items by position only once someone reordered it.
--
--   user_playlists.hand_ordered  set by PUT /api/me/playlists/{id}/order
--   collections.hand_ordered     set by PATCH /api/collections/{id} with items
--
-- The lists reordered before this slot are found from their items: one placed
-- ahead of an item added before it was moved there by hand (items added
-- together share added_at, so a list made in one go and then reordered is
-- missed and opens newest-added first).
ALTER TABLE user_playlists ADD COLUMN IF NOT EXISTS hand_ordered boolean NOT NULL DEFAULT false;
ALTER TABLE collections ADD COLUMN IF NOT EXISTS hand_ordered boolean NOT NULL DEFAULT false;
UPDATE user_playlists p SET hand_ordered = true
 WHERE NOT p.hand_ordered AND EXISTS (
    SELECT 1 FROM user_playlist_items a
      JOIN user_playlist_items b ON b.playlist_id = a.playlist_id
     WHERE a.playlist_id = p.id AND a.position < b.position AND a.added_at > b.added_at);
UPDATE collections c SET hand_ordered = true
 WHERE NOT c.hand_ordered AND NOT c.is_smart AND EXISTS (
    SELECT 1 FROM collection_items a
      JOIN collection_items b ON b.collection_id = a.collection_id
     WHERE a.collection_id = c.id AND a.position < b.position AND a.added_at > b.added_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE collections DROP COLUMN IF EXISTS hand_ordered;
ALTER TABLE user_playlists DROP COLUMN IF EXISTS hand_ordered;
-- +goose StatementEnd
