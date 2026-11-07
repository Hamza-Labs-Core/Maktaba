-- name: generation.pool_overlay
-- mode: read
-- scope: catalog
-- tables: filler_items
SELECT id, video_id, duration_ms FROM filler_items WHERE pool_id = $1
