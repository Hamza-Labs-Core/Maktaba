-- name: youtube_snapshot.snapshot.3
-- mode: read
-- scope: library $1
-- tables: youtube_channels, youtube_playlist_items, youtube_playlists
SELECT i.playlist_id AS playlist_id, i.youtube_id AS youtube_id, i.position AS position FROM youtube_playlist_items i JOIN youtube_playlists p ON p.playlist_id = i.playlist_id WHERE p.channel_id IN (SELECT channel_id FROM youtube_channels WHERE library_id = $1) ORDER BY i.playlist_id, i.youtube_id
