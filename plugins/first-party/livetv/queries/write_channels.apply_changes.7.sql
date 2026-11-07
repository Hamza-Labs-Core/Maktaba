-- name: write_channels.apply_changes.7
-- mode: write
-- scope: catalog
-- tables: channel_schedule_state*
INSERT INTO channel_schedule_state (channel_id, stale, sched_gen) VALUES ($1, $2, $3) ON CONFLICT (channel_id, sched_gen) DO UPDATE SET stale = EXCLUDED.stale
