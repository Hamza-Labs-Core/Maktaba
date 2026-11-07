-- name: gc.sweep.2
-- mode: read
-- scope: catalog
-- tables: channel_schedule_state
SELECT DISTINCT sched_gen AS g FROM channel_schedule_state
