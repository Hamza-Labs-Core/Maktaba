-- name: extract.commit_extract.3
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT state FROM videos WHERE id = $1
