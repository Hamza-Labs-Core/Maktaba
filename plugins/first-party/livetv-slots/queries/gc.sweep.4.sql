-- name: gc.sweep.4
-- mode: write
-- scope: catalog
-- tables: channel_programs*
DELETE FROM channel_programs WHERE sched_gen = $1
