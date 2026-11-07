-- name: repo.stale_channels
-- mode: read
-- scope: catalog
-- tables: channel_schedule_state, channels
SELECT s.channel_id AS id FROM channel_schedule_state s JOIN channels c ON c.id = s.channel_id AND c.lifecycle = 'live' WHERE s.sched_gen = $1 AND s.stale = true
