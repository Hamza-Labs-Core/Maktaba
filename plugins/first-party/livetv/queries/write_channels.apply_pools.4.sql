-- name: write_channels.apply_pools.4
-- mode: write
-- scope: catalog
-- tables: filler_pools*
UPDATE filler_pools SET name = $2, updated_at = $3 WHERE id = $1
