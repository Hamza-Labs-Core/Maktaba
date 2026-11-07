-- name: repo.future_programs
-- mode: read
-- scope: catalog
-- tables: channel_programs
SELECT video_id FROM channel_programs WHERE channel_id = $1 AND start_at >= $2 AND sched_gen = $3 AND kind = 'program' AND video_id IS NOT NULL ORDER BY start_at
