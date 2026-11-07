-- name: subtitle_gen.input_fingerprint
-- mode: read
-- scope: subject $1
-- tables: transcripts
SELECT id, COALESCE(revision, 0) AS revision FROM transcripts WHERE video_id = $1 AND is_active ORDER BY created_at DESC LIMIT 1
