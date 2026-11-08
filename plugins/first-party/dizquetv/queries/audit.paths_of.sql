-- name: audit.paths_of
-- mode: read
-- scope: catalog
-- tables: videos
SELECT id, path FROM videos WHERE id = ANY($1::uuid[])
