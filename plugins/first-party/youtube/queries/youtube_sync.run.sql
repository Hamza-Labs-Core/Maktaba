-- name: youtube_sync.run
-- mode: read
-- scope: library $1
-- tables: youtube_channels
SELECT channel_id, url, status FROM youtube_channels WHERE library_id = $1 ORDER BY channel_id
