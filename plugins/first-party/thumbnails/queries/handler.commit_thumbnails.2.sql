-- name: handler.commit_thumbnails.2
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT state FROM videos WHERE id = $1
