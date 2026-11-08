-- name: dedup_merge.fail_group
-- mode: write
-- scope: dup_group $1
-- tables: duplicate_groups*
UPDATE duplicate_groups SET status = 'failed', reason = $2 WHERE id = $1
