-- name: repo.replace_future_blocks.3
-- mode: write
-- scope: catalog
-- tables: videos, channel_programs*
UPDATE channel_programs SET age_tier = (SELECT v.age_tier FROM videos v WHERE v.id = channel_programs.video_id) WHERE channel_id = $1 AND sched_gen = $3 AND start_at >= $2
