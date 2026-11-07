-- name: handler.commit_thumbnails
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET poster_path = $2, sprite_path = $3 WHERE id = $1
