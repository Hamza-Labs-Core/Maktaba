-- name: extract.commit_extract.3
-- mode: read
-- scope: catalog
-- tables: videos
SELECT state FROM videos WHERE id = $1
