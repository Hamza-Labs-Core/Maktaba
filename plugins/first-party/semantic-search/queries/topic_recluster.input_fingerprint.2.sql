-- name: topic_recluster.input_fingerprint.2
-- mode: read
-- scope: library $1
-- tables: video_embeddings
SELECT COUNT(*) AS n, MAX(updated_at) AS at FROM video_embeddings WHERE library_id = $1 AND model = $2
