-- name: probe.run
-- mode: read
-- scope: catalog
-- tables: videos
SELECT path FROM videos WHERE id = $1
