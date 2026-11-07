-- name: gc.sweep.7
-- mode: write
-- scope: catalog
-- tables: channels*
DELETE FROM channels WHERE id = $1
