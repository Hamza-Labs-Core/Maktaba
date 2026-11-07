-- name: transcribe.carry_over_edits
-- mode: read
-- scope: subject $1
-- tables: transcripts
SELECT id FROM transcripts WHERE video_id = $1 AND is_active AND id <> $2
