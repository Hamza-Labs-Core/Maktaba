-- name: gc.sweep.3
-- mode: read
-- scope: catalog
-- tables: channel_programs
SELECT count(*) FROM channel_programs WHERE sched_gen = $1
