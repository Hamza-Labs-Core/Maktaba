-- name: repo.max_seq_before
-- mode: read
-- scope: catalog
-- tables: channel_programs
SELECT max(seq) AS m FROM channel_programs WHERE channel_id = $1 AND sched_gen = $3 AND start_at < $2
