-- name: trash.trash_days
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT settings FROM libraries WHERE id = $1
