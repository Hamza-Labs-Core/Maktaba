-- name: duplicates.write_groups.9
-- mode: read
-- scope: subject $1
-- tables: duplicate_groups, duplicate_members
SELECT DISTINCT d.id AS id, d.members_key AS members_key FROM duplicate_groups d JOIN duplicate_members m ON m.group_id = d.id WHERE d.status = 'open' AND m.video_id = $1
