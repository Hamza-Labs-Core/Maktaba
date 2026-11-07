-- name: index.active_transcript
-- mode: read
-- scope: subject $1
-- tables: transcript_segments, transcripts
SELECT t.id AS id, COALESCE(t.revision, 0) AS revision, (SELECT COUNT(*) FROM transcript_segments s WHERE s.transcript_id = t.id) AS n FROM transcripts t WHERE t.video_id = $1 AND t.is_active ORDER BY t.created_at DESC, t.id DESC LIMIT 1
