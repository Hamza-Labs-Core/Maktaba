-- name: convert_mp4.run_video.3
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET full_hash = NULL WHERE id = $1
