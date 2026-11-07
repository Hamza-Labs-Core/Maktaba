-- name: index.video_language
-- mode: read
-- scope: catalog
-- tables: videos
SELECT detected_language FROM videos WHERE id = $1
