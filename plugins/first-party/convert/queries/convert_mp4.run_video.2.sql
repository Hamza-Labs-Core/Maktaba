-- name: convert_mp4.run_video.2
-- mode: read
-- scope: catalog
-- tables: videos
SELECT content_hash FROM videos WHERE id = $1
