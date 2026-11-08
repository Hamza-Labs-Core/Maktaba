-- name: pgstore.delete_video
-- mode: write
-- scope: subject $1
-- tables: segment_embeddings*
DELETE FROM segment_embeddings WHERE video_id = $1::uuid
