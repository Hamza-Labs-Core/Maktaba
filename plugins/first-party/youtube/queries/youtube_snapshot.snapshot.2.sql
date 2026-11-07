-- name: youtube_snapshot.snapshot.2
-- mode: read
-- scope: library $1
-- tables: youtube_channels, youtube_playlists
SELECT playlist_id, channel_id, title, item_count, is_real FROM youtube_playlists WHERE channel_id IN (SELECT channel_id FROM youtube_channels WHERE library_id = $1) ORDER BY playlist_id
