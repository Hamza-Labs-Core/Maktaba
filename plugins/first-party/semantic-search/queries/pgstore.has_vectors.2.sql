-- name: pgstore.has_vectors.2
-- mode: read
-- scope: subject $1, transcript $2
-- tables: segment_embeddings, transcript_segments
SELECT (SELECT count(*) FROM segment_embeddings e JOIN transcript_segments s ON s.id = e.segment_id WHERE e.video_id = $1::uuid AND s.transcript_id = $2::uuid) >= (SELECT count(*) FROM transcript_segments s WHERE s.transcript_id = $2::uuid AND btrim(s.text) <> '')
