-- name: collections_import.run_collections_import
-- mode: read
-- scope: library $1
-- tables: plex_items
SELECT DISTINCT section_key FROM plex_items WHERE library_id = $1 ORDER BY section_key
