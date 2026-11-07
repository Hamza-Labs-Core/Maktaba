-- name: repo.channels_needing_topup.2
-- mode: read
-- scope: catalog
-- tables: channel_schedule_state, channels
SELECT c.id AS id
            FROM channels c
            LEFT JOIN channel_schedule_state s ON s.channel_id = c.id AND s.sched_gen = $2
            WHERE c.enabled = true AND c.lifecycle = 'live'
              AND (s.horizon_until IS NULL OR s.horizon_until < $1 OR s.stale = true)
