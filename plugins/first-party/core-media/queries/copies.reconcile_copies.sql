-- name: copies.reconcile_copies
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT roots FROM libraries WHERE id = $1
