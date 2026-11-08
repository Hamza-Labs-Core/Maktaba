-- name: index.video_language
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT detected_language FROM videos WHERE id = $1
