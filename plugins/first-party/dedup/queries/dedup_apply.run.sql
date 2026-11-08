-- name: dedup_apply.run
-- mode: read
-- scope: dup_group $1
-- tables: duplicate_groups
SELECT library_id, tier, status, members_key FROM duplicate_groups WHERE id = $1
