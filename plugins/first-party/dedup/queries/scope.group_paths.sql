-- name: scope.group_paths
-- mode: read
-- scope: catalog
-- tables: duplicate_members
SELECT path FROM duplicate_members WHERE group_id = $1
