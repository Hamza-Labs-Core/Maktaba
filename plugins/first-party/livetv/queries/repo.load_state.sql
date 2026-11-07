-- name: repo.load_state
-- mode: read
-- scope: catalog
-- tables: channel_schedule_state
SELECT channel_id, anchor_at, horizon_until, last_generated_at, cursor, stale
            FROM channel_schedule_state WHERE channel_id = $1 AND sched_gen = $2
