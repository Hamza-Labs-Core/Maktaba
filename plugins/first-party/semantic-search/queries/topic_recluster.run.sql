-- name: topic_recluster.run
-- mode: read
-- scope: library $1
-- tables: video_embeddings, videos
SELECT e.video_id, e.dim, e.vec, COALESCE(v.title, v.filename) AS title FROM video_embeddings e JOIN videos v ON v.id = e.video_id WHERE e.library_id = $1 AND e.model = $2 AND v.deleted_at IS NULL ORDER BY e.video_id
