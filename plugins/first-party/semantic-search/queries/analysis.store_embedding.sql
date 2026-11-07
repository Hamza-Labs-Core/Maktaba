-- name: analysis.store_embedding
-- mode: write
-- scope: subject $1
-- tables: video_embeddings*
INSERT INTO video_embeddings (video_id, library_id, transcript_id, model, dim, segments, vec, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (video_id) DO UPDATE SET library_id = EXCLUDED.library_id, transcript_id = EXCLUDED.transcript_id, model = EXCLUDED.model, dim = EXCLUDED.dim, segments = EXCLUDED.segments, vec = EXCLUDED.vec, updated_at = EXCLUDED.updated_at WHERE video_embeddings.library_id <> EXCLUDED.library_id OR video_embeddings.transcript_id <> EXCLUDED.transcript_id OR video_embeddings.model <> EXCLUDED.model OR video_embeddings.segments <> EXCLUDED.segments OR video_embeddings.vec <> EXCLUDED.vec
