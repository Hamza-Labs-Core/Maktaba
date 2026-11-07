-- name: sourced_collections.sync_sourced.3
-- mode: write
-- scope: catalog
-- tables: collections*
DELETE FROM collections WHERE id = $1
