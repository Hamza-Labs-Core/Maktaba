-- name: plan_inputs.load_inputs.6
-- mode: read
-- scope: catalog
-- tables: clip_group_items, clip_groups
SELECT i.video_id AS vid, g.title AS title, g.topic AS topic FROM clip_group_items i JOIN clip_groups g ON g.id = i.group_id
