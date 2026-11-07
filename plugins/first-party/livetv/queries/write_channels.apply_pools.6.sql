-- name: write_channels.apply_pools.6
-- mode: write
-- scope: subject $2
-- tables: filler_items*
UPDATE filler_items SET duration_ms = $3 WHERE pool_id = $1 AND video_id = $2
