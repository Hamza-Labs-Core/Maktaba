-- name: second_listen.segments
-- mode: read
-- scope: catalog
-- tables: transcript_segments
SELECT seq, start_sec, end_sec, text, confidence, metadata, word_data FROM transcript_segments WHERE transcript_id = $1 ORDER BY seq
