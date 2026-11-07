-- name: chapter_thumbs.content_hash
-- mode: read
-- scope: catalog
-- tables: videos
SELECT content_hash FROM videos WHERE id = $1
