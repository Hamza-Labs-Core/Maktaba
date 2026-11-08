-- name: chapter_infer.video_vectors
-- mode: read
-- scope: transcript $1
-- tables: transcript_segments
SELECT seq, text FROM transcript_segments WHERE transcript_id = $1
