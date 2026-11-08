-- name: duplicates.write_groups.7
-- mode: write
-- scope: dup_group $1, library $2, subject $3
-- tables: duplicate_groups*
UPDATE duplicate_groups SET library_id = $2, keeper_video_id = $3, members_key = $4, reason = $5 WHERE id = $1
