-- name: duplicates.auto_exact
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT settings FROM libraries WHERE id = $1
