-- name: repair_broken.record
-- mode: write
-- scope: subject $1
-- tables: video_repairs*
INSERT INTO video_repairs (video_id, library_id, youtube_id, state, message, backup_path, attempts, updated_at) VALUES ($1,$2,$3,$4,$5,$6,1,$7) ON CONFLICT (video_id) DO UPDATE SET library_id = EXCLUDED.library_id, youtube_id = EXCLUDED.youtube_id, state = EXCLUDED.state, message = EXCLUDED.message, backup_path = EXCLUDED.backup_path, attempts = video_repairs.attempts + 1, updated_at = EXCLUDED.updated_at
