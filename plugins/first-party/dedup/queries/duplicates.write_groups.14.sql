-- name: duplicates.write_groups.14
-- mode: write
-- scope: dup_group $1
-- tables: duplicate_groups*
DELETE FROM duplicate_groups WHERE id = $1
