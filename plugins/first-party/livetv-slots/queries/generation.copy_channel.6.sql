-- name: generation.copy_channel.6
-- mode: read
-- scope: catalog
-- tables: channel_programs
SELECT count(*) FROM channel_programs WHERE channel_id = $1 AND sched_gen = $2
