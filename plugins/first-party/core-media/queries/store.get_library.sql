-- name: store.get_library
-- mode: read
-- scope: library $1
-- tables: libraries
SELECT id, name, roots, settings
  FROM libraries
 WHERE id = $1
