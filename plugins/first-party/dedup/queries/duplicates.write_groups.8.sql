-- name: duplicates.write_groups.8
-- mode: write
-- scope: subject $2
-- tables: duplicate_members*
INSERT INTO duplicate_members (group_id, video_id, path, is_copy, score) VALUES ($1, $2, $3, $4, $5)
