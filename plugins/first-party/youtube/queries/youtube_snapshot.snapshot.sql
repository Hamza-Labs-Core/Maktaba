-- name: youtube_snapshot.snapshot
-- mode: read
-- scope: library $1
-- tables: youtube_channels, youtube_videos
SELECT youtube_id, channel_id, title, CAST(upload_date AS TEXT) AS d, date_quality, duration_s, tab FROM youtube_videos WHERE channel_id IN (SELECT channel_id FROM youtube_channels WHERE library_id = $1) ORDER BY youtube_id
