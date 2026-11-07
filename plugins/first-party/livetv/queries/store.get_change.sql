-- name: store.get_change
-- mode: read
-- scope: catalog
-- tables: tv_changes
SELECT * FROM tv_changes WHERE id = $1
