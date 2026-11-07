-- name: generation.copy_channel
-- mode: write
-- scope: catalog
-- tables: channel_programs*
DELETE FROM channel_programs WHERE channel_id = $1 AND sched_gen = $2
