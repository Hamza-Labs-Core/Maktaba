-- name: convert_mp4.run_video
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT content_hash, poster_path, sprite_path FROM videos WHERE id = $1
