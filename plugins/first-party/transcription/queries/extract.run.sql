-- name: extract.run
-- mode: read
-- scope: catalog
-- tables: videos
SELECT path, content_hash FROM videos WHERE id = $1
