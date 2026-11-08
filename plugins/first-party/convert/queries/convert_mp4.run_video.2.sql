-- name: convert_mp4.run_video.2
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT content_hash FROM videos WHERE id = $1
