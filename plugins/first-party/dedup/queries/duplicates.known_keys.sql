-- name: duplicates.known_keys
-- mode: read
-- scope: catalog
-- tables: duplicate_groups
SELECT members_key, status, created_at FROM duplicate_groups WHERE members_key = ANY($1::text[]) ORDER BY created_at
