-- name: clip_groups.run.3
-- mode: write
-- scope: subject $2
-- tables: clip_group_items*
INSERT INTO clip_group_items (group_id, video_id, position) VALUES ($1,$2,$3)
