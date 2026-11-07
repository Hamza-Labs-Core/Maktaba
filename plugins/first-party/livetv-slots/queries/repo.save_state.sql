-- name: repo.save_state
-- mode: write
-- scope: catalog
-- tables: channel_schedule_state*
INSERT INTO channel_schedule_state
                (channel_id, anchor_at, horizon_until, last_generated_at, cursor, stale, sched_gen)
            VALUES ($1,$2,$3,$4,$5,false,$6)
            ON CONFLICT (channel_id, sched_gen) DO UPDATE SET
                anchor_at = EXCLUDED.anchor_at,
                horizon_until = EXCLUDED.horizon_until,
                last_generated_at = EXCLUDED.last_generated_at,
                cursor = EXCLUDED.cursor,
                stale = false
