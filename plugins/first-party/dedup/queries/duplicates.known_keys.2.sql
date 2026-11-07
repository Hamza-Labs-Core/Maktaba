-- name: duplicates.known_keys.2
-- mode: read
-- scope: catalog
-- tables: duplicate_groups
SELECT members_key, status, created_at FROM duplicate_groups WHERE members_key IN (SELECT value FROM json_each($1)) ORDER BY created_at
