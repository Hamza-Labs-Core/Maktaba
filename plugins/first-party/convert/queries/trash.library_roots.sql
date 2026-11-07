-- name: trash.library_roots
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT roots FROM libraries WHERE id = $1
