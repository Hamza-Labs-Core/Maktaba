-- name: convert_mp4.run_video
-- mode: read
-- scope: catalog
-- tables: videos
SELECT content_hash, poster_path, sprite_path FROM videos WHERE id = $1
