-- name: duplicates.write_groups.8
-- mode: write
-- scope: catalog
-- tables: duplicate_members*
DELETE FROM duplicate_members WHERE group_id = $1
