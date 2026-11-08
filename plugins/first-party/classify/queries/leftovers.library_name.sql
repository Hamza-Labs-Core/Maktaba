-- name: leftovers.library_name
-- mode: read
-- scope: library $1
-- tables: libraries
SELECT name FROM libraries WHERE id = $1
