-- name: pgstore.delete_video
-- mode: write
-- scope: catalog
-- tables: segment_embeddings*
DELETE FROM segment_embeddings WHERE video_id = $1::uuid
