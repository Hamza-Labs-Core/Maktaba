-- name: repo.replace_future_blocks
-- mode: write
-- scope: catalog
-- tables: channel_programs*
DELETE FROM channel_programs WHERE channel_id = $1 AND sched_gen = $3 AND start_at >= $2
