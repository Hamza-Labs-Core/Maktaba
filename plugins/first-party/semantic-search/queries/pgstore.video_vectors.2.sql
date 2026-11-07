-- name: pgstore.video_vectors.2
-- mode: read
-- scope: catalog
-- tables: segment_embeddings, transcript_segments
SELECT e.segment_id, s.transcript_id, s.seq, s.start_sec, s.end_sec, s.text, e.embedding::text AS embedding FROM segment_embeddings e JOIN transcript_segments s ON s.id = e.segment_id WHERE e.video_id = $1::uuid AND s.transcript_id = $2::uuid ORDER BY s.seq
