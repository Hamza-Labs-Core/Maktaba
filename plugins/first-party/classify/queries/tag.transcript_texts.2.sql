-- name: tag.transcript_texts.2
-- mode: read
-- scope: transcript $2
-- tables: transcript_segments
SELECT transcript_id AS tid, text FROM transcript_segments WHERE start_sec < $1 AND transcript_id IN (SELECT value FROM json_each($2)) ORDER BY transcript_id, seq
