-- name: analysis.drop_embedding
-- mode: write
-- scope: subject $1
-- tables: video_embeddings*
DELETE FROM video_embeddings WHERE video_id = $1
