-- name: legacy.probe_output
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT duration_sec, metadata FROM videos WHERE id = $1
