-- name: groups.planned
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT name FROM libraries WHERE id = $1
