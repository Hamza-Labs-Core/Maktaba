-- name: repo.airing_until
-- mode: read
-- scope: catalog
-- tables: channel_programs
SELECT max(end_at) AS e FROM channel_programs WHERE channel_id = $1 AND sched_gen = $3 AND start_at <= $2 AND end_at > $2
