-- name: store.get_library
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT id, name, roots, settings
  FROM libraries
 WHERE id = $1
