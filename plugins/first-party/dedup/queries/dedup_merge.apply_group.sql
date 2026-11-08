-- name: dedup_merge.apply_group
-- mode: read
-- scope: dup_group $1
-- tables: duplicate_groups
SELECT id, library_id, tier, status, keeper_video_id FROM duplicate_groups WHERE id = $1
