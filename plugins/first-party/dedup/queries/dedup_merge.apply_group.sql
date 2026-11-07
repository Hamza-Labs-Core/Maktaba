-- name: dedup_merge.apply_group
-- mode: read
-- scope: catalog
-- tables: duplicate_groups
SELECT id, library_id, tier, status, keeper_video_id FROM duplicate_groups WHERE id = $1
