-- name: tag.transcript_texts
-- mode: read
-- scope: transcript $2
-- tables: transcript_segments
SELECT transcript_id AS tid, text FROM transcript_segments WHERE start_sec < $1 AND transcript_id = ANY($2::uuid[]) ORDER BY transcript_id, seq
