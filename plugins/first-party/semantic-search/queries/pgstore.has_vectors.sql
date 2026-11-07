-- name: pgstore.has_vectors
-- mode: read
-- scope: catalog
-- tables: segment_embeddings
SELECT 1 FROM segment_embeddings WHERE video_id = $1::uuid LIMIT 1
