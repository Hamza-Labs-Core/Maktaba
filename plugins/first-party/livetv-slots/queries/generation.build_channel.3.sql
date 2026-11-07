-- name: generation.build_channel.3
-- mode: write
-- scope: catalog
-- tables: channel_programs*
UPDATE channel_programs SET end_at = $3 WHERE channel_id = $1 AND sched_gen = $2 AND start_at < $3 AND end_at > $3
