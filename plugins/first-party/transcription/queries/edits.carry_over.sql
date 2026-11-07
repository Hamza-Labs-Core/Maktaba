-- name: edits.carry_over
-- mode: read
-- scope: catalog
-- tables: transcript_edits, transcript_segments
SELECT e.id, e.segment_seq, e.before, e.after, e.user_id, s.start_sec FROM transcript_edits e JOIN transcript_segments s   ON s.transcript_id = e.transcript_id AND s.seq = e.segment_seq WHERE e.transcript_id = $1 AND e.undone_at IS NULL ORDER BY e.created_at, e.id
