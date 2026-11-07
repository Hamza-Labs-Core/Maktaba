-- name: repo.clear_stale
-- mode: write
-- scope: catalog
-- tables: channel_schedule_state*
UPDATE channel_schedule_state SET stale = $3 WHERE channel_id = $1 AND sched_gen = $2
