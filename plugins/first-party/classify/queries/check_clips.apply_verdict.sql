-- name: check_clips.apply_verdict
-- mode: write
-- scope: subject $1
-- tables: clip_checks*
UPDATE clip_checks SET applied_hash = $2 WHERE video_id = $1
