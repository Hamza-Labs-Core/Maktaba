-- name: analysis.load_embedding
-- mode: read
-- scope: subject $1
-- tables: video_embeddings
SELECT video_id, library_id, transcript_id, model, dim, segments, vec FROM video_embeddings WHERE video_id = $1
