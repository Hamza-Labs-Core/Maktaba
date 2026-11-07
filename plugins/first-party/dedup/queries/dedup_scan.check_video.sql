-- name: dedup_scan.check_video
-- mode: read
-- scope: catalog
-- tables: videos
SELECT library_id FROM videos WHERE id = $1
