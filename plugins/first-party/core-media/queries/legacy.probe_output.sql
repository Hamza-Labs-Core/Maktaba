-- name: legacy.probe_output
-- mode: read
-- scope: catalog
-- tables: videos
SELECT duration_sec, metadata FROM videos WHERE id = $1
