-- name: sourced_collections.write_items
-- mode: write
-- scope: catalog
-- tables: collection_items*
DELETE FROM collection_items WHERE collection_id = $1
