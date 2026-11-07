-- name: generation.copy_channel.5
-- mode: write
-- scope: catalog
-- tables: channel_schedule_state*
INSERT INTO channel_schedule_state (channel_id, sched_gen, anchor_at, horizon_until, last_generated_at, generator_version, cursor, stale) SELECT channel_id, $3, anchor_at, horizon_until, last_generated_at, generator_version, cursor, stale FROM channel_schedule_state WHERE channel_id = $1 AND sched_gen = $2
