-- name: link.candidates.2
-- mode: read
-- scope: library $1
-- tables: youtube_channel_libraries, youtube_channels, youtube_videos
SELECT yv.youtube_id, yv.title, yv.duration_s FROM youtube_videos yv WHERE yv.channel_id IN (SELECT channel_id FROM youtube_channels WHERE library_id = $1 UNION SELECT channel_id FROM youtube_channel_libraries WHERE library_id = $1) ORDER BY yv.youtube_id
