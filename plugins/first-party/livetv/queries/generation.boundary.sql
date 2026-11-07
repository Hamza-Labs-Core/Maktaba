-- name: generation.boundary
-- mode: read
-- scope: catalog
-- tables: channel_programs
SELECT max(end_at) FROM channel_programs WHERE channel_id = $1 AND sched_gen = $2 AND start_at <= $3 AND end_at > $3
