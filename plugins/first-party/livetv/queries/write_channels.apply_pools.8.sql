-- name: write_channels.apply_pools.8
-- mode: write
-- scope: catalog
-- tables: filler_items*
DELETE FROM filler_items WHERE pool_id = $1 AND video_id IN (SELECT value FROM json_each($2))
