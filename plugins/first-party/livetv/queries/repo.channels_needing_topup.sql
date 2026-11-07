-- name: repo.channels_needing_topup
-- mode: read
-- scope: catalog
-- tables: channel_schedule_state
SELECT channel_id, horizon_until, stale FROM channel_schedule_state WHERE sched_gen = $1
