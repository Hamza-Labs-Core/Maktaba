-- name: audit.paths_of.2
-- mode: read
-- scope: catalog
-- tables: videos
SELECT id, path FROM videos WHERE id IN (SELECT value FROM json_each($1))
