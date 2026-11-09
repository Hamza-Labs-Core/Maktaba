-- name: scope.group_paths
-- mode: read
-- scope: dup_group $1
-- tables: duplicate_members
SELECT path FROM duplicate_members WHERE group_id = $1
