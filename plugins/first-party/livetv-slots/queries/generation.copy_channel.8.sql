-- name: generation.copy_channel.8
-- mode: write
-- scope: catalog
-- tables: channel_programs*
INSERT INTO channel_programs (id, channel_id, seq, kind, video_id, filler_item_id, start_at, end_at, source_offset, source_duration, title_snapshot, source_url, age_tier, sched_gen) SELECT lower(hex(randomblob(4)) || '-' || hex(randomblob(2)) || '-4' || substr(hex(randomblob(2)), 2) || '-' || substr('89ab', 1 + (abs(random()) % 4), 1) || substr(hex(randomblob(2)), 2) || '-' || hex(randomblob(6))), channel_id, seq, kind, video_id, filler_item_id, start_at, end_at, source_offset, source_duration, title_snapshot, source_url, age_tier, $3 FROM channel_programs WHERE channel_id = $1 AND sched_gen = $2
