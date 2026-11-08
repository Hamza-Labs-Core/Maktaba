-- name: check_clips.run
-- mode: write
-- scope: subject $1
-- tables: clip_checks*
INSERT INTO clip_checks (video_id, input_hash, model, topics, applied_hash, checked_at) VALUES ($1,$2,$3,$4,NULL,$5) ON CONFLICT (video_id) DO UPDATE SET input_hash = EXCLUDED.input_hash, model = EXCLUDED.model, topics = EXCLUDED.topics, applied_hash = NULL, checked_at = EXCLUDED.checked_at
