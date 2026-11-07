-- name: pgstore.keep_only
-- mode: write
-- scope: catalog
-- tables: segment_embeddings*
DELETE FROM segment_embeddings WHERE video_id = $1::uuid AND segment_id <> ALL($2::bigint[])
