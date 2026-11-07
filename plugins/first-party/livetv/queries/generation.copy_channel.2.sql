-- name: generation.copy_channel.2
-- mode: write
-- scope: catalog
-- tables: channel_schedule_state*
DELETE FROM channel_schedule_state WHERE channel_id = $1 AND sched_gen = $2
