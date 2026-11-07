-- name: store.transition.2
-- mode: write
-- scope: catalog
-- tables: tv_changes*
UPDATE tv_changes SET state = $2, updated_at = $4 WHERE id = $1 AND state = $3 RETURNING id
