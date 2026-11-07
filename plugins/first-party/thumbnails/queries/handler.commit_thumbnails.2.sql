-- name: handler.commit_thumbnails.2
-- mode: read
-- scope: catalog
-- tables: videos
SELECT state FROM videos WHERE id = $1
