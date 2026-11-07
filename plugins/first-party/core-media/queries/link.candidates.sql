-- name: link.candidates
-- mode: read
-- scope: library $1
-- tables: youtube_channel_libraries, youtube_channels, youtube_videos
SELECT count(*) AS n, max(yv.fetched_at) AS m FROM youtube_videos yv WHERE yv.channel_id IN (SELECT channel_id FROM youtube_channels WHERE library_id = $1 UNION SELECT channel_id FROM youtube_channel_libraries WHERE library_id = $1)
