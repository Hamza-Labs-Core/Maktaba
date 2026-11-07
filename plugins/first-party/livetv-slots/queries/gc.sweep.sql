-- name: gc.sweep
-- mode: read
-- scope: catalog
-- tables: channel_programs
SELECT DISTINCT sched_gen AS g FROM channel_programs
