-- name: sourced_collections.free_name
-- mode: read
-- scope: library $1
-- tables: collections
SELECT id, name FROM collections WHERE library_id = $1
