-- name: gc.sweep.5
-- mode: write
-- scope: catalog
-- tables: channel_schedule_state*
DELETE FROM channel_schedule_state WHERE sched_gen = $1
