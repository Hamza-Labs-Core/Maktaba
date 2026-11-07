-- name: leftovers.library_name
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT name FROM libraries WHERE id = $1
