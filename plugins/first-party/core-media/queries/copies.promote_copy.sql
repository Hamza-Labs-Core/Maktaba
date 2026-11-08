-- name: copies.promote_copy
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT state FROM videos WHERE id = $1
