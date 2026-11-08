-- name: repo.filler_pools.2
-- mode: read
-- scope: catalog
-- tables: filler_items, videos
SELECT fi.id, fi.pool_id, fi.video_id, fi.type, fi.duration_ms, v.duration_sec, v.title FROM filler_items fi JOIN videos v ON v.id = fi.video_id WHERE fi.pool_id = ANY($1::uuid[]) AND v.deleted_at IS NULL ORDER BY fi.pool_id, fi.id
