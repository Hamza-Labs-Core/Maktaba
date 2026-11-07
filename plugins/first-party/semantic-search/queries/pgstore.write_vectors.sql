-- name: pgstore.write_vectors
-- mode: write
-- scope: catalog
-- tables: transcript_segments, segment_embeddings*
INSERT INTO segment_embeddings (segment_id, video_id, library_id, language, model, embedding) SELECT u.segment_id, $1::uuid, $2::uuid, $3, $4, u.embedding::halfvec FROM unnest($5::bigint[], $6::text[]) AS u(segment_id, embedding) JOIN transcript_segments s ON s.id = u.segment_id ON CONFLICT (segment_id) DO UPDATE SET video_id = EXCLUDED.video_id, library_id = EXCLUDED.library_id, language = EXCLUDED.language, model = EXCLUDED.model, embedding = EXCLUDED.embedding
