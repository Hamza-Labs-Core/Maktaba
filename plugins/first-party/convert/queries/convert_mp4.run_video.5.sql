-- name: convert_mp4.run_video.5
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET poster_path = $2, sprite_path = $3 WHERE id = $1
