-- name: swap.restore_dedup.2
-- mode: write
-- scope: catalog
-- tables: videos*
UPDATE videos SET superseded_by = NULL, state = $2 WHERE id = $1 AND state = 'superseded'
