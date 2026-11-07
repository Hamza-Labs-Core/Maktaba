-- name: duplicates.write_groups.9
-- mode: write
-- scope: library $2
-- tables: duplicate_groups*
INSERT INTO duplicate_groups (id, library_id, tier, status, keeper_video_id, members_key, reason, created_at) VALUES ($1, $2, $3, 'open', $4, $5, $6, $7)
