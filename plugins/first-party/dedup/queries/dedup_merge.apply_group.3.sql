-- name: dedup_merge.apply_group.3
-- mode: write
-- scope: subject $1
-- tables: video_copies*
DELETE FROM video_copies WHERE video_id = $1 AND path = $2
