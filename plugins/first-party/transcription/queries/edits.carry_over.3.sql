-- name: edits.carry_over.3
-- mode: read
-- scope: catalog
-- tables: transcript_segments
SELECT seq, start_sec, end_sec, text, word_data FROM transcript_segments WHERE transcript_id = $1 AND end_sec >= $2 AND start_sec <= $3 ORDER BY seq
