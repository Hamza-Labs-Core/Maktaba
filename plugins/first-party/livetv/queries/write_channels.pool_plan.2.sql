-- name: write_channels.pool_plan.2
-- mode: read
-- scope: catalog
-- tables: filler_items
SELECT video_id, duration_ms FROM filler_items WHERE pool_id = $1
