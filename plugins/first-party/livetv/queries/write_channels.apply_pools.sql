-- name: write_channels.apply_pools
-- mode: write
-- scope: catalog
-- tables: filler_items*
DELETE FROM filler_items WHERE pool_id = $1
