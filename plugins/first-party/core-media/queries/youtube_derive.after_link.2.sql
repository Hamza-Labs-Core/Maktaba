-- name: youtube_derive.after_link.2
-- mode: read
-- scope: catalog
-- tables: youtube_playlist_items, youtube_playlists
SELECT i.playlist_id AS pid FROM youtube_playlist_items i JOIN youtube_playlists p ON p.playlist_id = i.playlist_id WHERE i.youtube_id = $1 AND p.is_real
