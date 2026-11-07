-- name: thumbnail.run
-- mode: read
-- scope: catalog
-- tables: videos
SELECT path, content_hash, duration_sec FROM videos WHERE id = $1
