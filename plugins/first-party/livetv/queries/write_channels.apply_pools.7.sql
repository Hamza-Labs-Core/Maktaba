-- name: write_channels.apply_pools.7
-- mode: write
-- scope: catalog
-- tables: filler_items*
DELETE FROM filler_items WHERE pool_id = $1 AND video_id = ANY($2::uuid[])
