-- name: pgstore.keep_only
-- mode: write
-- scope: subject $1
-- tables: segment_embeddings*
DELETE FROM segment_embeddings WHERE video_id = $1::uuid AND segment_id <> ALL($2::bigint[])
