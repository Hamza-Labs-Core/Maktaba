-- name: tag.playlists_of
-- mode: read
-- scope: catalog
-- tables: youtube_playlist_items
SELECT playlist_id, youtube_id FROM youtube_playlist_items WHERE youtube_id = ANY($1::text[]) ORDER BY playlist_id
