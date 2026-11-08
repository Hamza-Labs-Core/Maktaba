-- name: thumbnail.run
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT path, content_hash, duration_sec FROM videos WHERE id = $1
