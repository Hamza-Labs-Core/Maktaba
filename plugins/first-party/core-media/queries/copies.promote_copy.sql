-- name: copies.promote_copy
-- mode: read
-- scope: catalog
-- tables: videos
SELECT state FROM videos WHERE id = $1
