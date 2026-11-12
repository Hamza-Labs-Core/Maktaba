-- name: generation.copy_channel.7
-- mode: write
-- scope: catalog
-- tables: channel_programs*
INSERT INTO channel_programs (id, channel_id, seq, kind, video_id, filler_item_id, start_at, end_at, source_offset, source_duration, title_snapshot, source_url, age_tier, sched_gen) SELECT gen_random_uuid(), channel_id, seq, kind, video_id, filler_item_id, start_at, end_at, source_offset, source_duration, title_snapshot, source_url, age_tier, $3 FROM channel_programs WHERE channel_id = $1 AND sched_gen = $2
