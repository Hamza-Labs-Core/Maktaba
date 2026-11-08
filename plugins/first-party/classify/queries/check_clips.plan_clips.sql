-- name: check_clips.plan_clips
-- mode: read
-- scope: library $1
-- tables: clip_checks, videos
SELECT c.video_id, c.input_hash, c.topics, c.applied_hash FROM clip_checks c JOIN videos v ON v.id = c.video_id WHERE v.library_id = $1
