-- name: index.run
-- mode: read
-- scope: catalog
-- tables: transcript_segments
SELECT id, seq, start_sec, end_sec, text FROM transcript_segments WHERE transcript_id = $1 ORDER BY seq
