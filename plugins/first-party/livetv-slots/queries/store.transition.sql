-- name: store.transition
-- mode: read
-- scope: catalog
-- tables: tv_changes
SELECT state FROM tv_changes WHERE id = $1
