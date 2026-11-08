-- name: duplicates.write_groups.4
-- mode: write
-- scope: dup_group $1
-- tables: duplicate_groups*
UPDATE duplicate_groups SET status = 'open', reason = $2 WHERE id = $1
