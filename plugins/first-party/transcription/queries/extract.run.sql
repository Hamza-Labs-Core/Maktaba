-- name: extract.run
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT path, content_hash FROM videos WHERE id = $1
