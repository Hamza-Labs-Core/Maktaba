-- name: repo.ref_items.3
-- mode: read
-- scope: catalog
-- tables: collection_items, videos
SELECT v.id, v.title, v.duration_sec FROM collection_items ci JOIN videos v ON v.id = ci.video_id WHERE ci.collection_id = $1::uuid AND v.deleted_at IS NULL AND v.duration_sec IS NOT NULL AND v.duration_sec > 0 ORDER BY ci.position, v.title
