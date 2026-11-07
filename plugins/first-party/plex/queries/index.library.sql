-- name: index.library
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT name, roots FROM libraries WHERE id = $1
