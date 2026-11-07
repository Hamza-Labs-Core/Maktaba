-- name: write_channels.apply_pools.5
-- mode: write
-- scope: subject $3
-- tables: filler_items*
INSERT INTO filler_items (id, pool_id, video_id, type, duration_ms, created_at) VALUES ($1, $2, $3, 'filler', $4, $5) ON CONFLICT (pool_id, video_id) DO NOTHING
