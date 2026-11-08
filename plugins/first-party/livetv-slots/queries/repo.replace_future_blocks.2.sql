-- name: repo.replace_future_blocks.2
-- mode: write
-- scope: catalog
-- tables: channel_programs*
INSERT INTO channel_programs
                        (channel_id, seq, kind, video_id, filler_item_id,
                         start_at, end_at, source_offset, source_duration, title_snapshot,
                         source_url, sched_gen)
                    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
