-- name: youtube_derive.after_link
-- mode: read
-- scope: youtube_video $1
-- tables: youtube_videos
SELECT channel_id FROM youtube_videos WHERE youtube_id = $1
