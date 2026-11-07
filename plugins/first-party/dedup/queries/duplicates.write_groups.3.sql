-- name: duplicates.write_groups.3
-- mode: read
-- scope: catalog
-- tables: duplicate_groups
SELECT id, status FROM duplicate_groups WHERE members_key = $1 ORDER BY created_at DESC LIMIT 1
