-- name: trash.trash_days
-- mode: read
-- scope: library $1
-- tables: libraries
SELECT settings FROM libraries WHERE id = $1
