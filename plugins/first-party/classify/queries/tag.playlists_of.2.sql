-- name: tag.playlists_of.2
-- mode: read
-- scope: catalog
-- tables: youtube_playlist_items
SELECT playlist_id, youtube_id FROM youtube_playlist_items WHERE youtube_id IN (SELECT value FROM json_each($1)) ORDER BY playlist_id
