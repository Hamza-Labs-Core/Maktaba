-- name: dedup_scan.check_video
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT library_id FROM videos WHERE id = $1
