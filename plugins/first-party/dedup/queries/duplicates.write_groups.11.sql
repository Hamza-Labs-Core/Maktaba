-- name: duplicates.write_groups.11
-- mode: read
-- scope: catalog
-- tables: duplicate_groups
SELECT id, members_key FROM duplicate_groups WHERE 1 = 1 AND status = 'open'
