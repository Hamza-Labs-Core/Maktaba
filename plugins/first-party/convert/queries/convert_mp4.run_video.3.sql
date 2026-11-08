-- name: convert_mp4.run_video.3
-- mode: write
-- scope: subject $1
-- tables: videos*
UPDATE videos SET full_hash = NULL WHERE id = $1
