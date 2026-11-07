-- name: duplicates.write_groups.7
-- mode: write
-- scope: catalog
-- tables: duplicate_groups*
UPDATE duplicate_groups SET library_id = $2, keeper_video_id = $3, members_key = $4, reason = $5 WHERE id = $1
