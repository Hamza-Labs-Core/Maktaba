-- name: duplicates.write_groups.4
-- mode: write
-- scope: catalog
-- tables: duplicate_groups*
UPDATE duplicate_groups SET status = 'open', reason = $2 WHERE id = $1
