-- name: duplicates.write_groups.10
-- mode: read
-- scope: library $1
-- tables: duplicate_groups
SELECT id, members_key FROM duplicate_groups WHERE library_id = $1 AND status = 'open'
