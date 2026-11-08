-- name: duplicates.write_groups.8
-- mode: write
-- scope: dup_group $1
-- tables: duplicate_members*
DELETE FROM duplicate_members WHERE group_id = $1
