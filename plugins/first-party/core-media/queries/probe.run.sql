-- name: probe.run
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT path FROM videos WHERE id = $1
