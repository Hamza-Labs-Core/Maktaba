-- name: handler.commit_thumbnails
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET poster_path = $2, sprite_path = $3 WHERE id = $1
