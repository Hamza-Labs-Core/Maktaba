-- name: subtitle_gen.active
-- mode: read
-- scope: subject $1
-- tables: transcripts
SELECT id, language FROM transcripts WHERE video_id = $1 AND is_active ORDER BY created_at DESC LIMIT 1
