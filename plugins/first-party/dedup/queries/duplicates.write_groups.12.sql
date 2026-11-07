-- name: duplicates.write_groups.12
-- mode: write
-- scope: catalog
-- tables: duplicate_groups*
DELETE FROM duplicate_groups WHERE id = $1
