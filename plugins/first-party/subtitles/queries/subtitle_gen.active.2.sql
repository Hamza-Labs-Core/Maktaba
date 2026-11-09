-- name: subtitle_gen.active.2
-- mode: read
-- scope: transcript $1
-- tables: transcript_segments
SELECT start_sec, end_sec, text FROM transcript_segments WHERE transcript_id = $1 ORDER BY seq
