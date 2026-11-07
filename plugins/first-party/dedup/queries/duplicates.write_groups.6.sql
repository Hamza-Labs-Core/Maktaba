-- name: duplicates.write_groups.6
-- mode: read
-- scope: catalog
-- tables: duplicate_groups, duplicate_members
SELECT d.id FROM duplicate_groups d JOIN duplicate_members m ON m.group_id = d.id WHERE d.status = 'open' AND d.tier = $1 AND m.video_id IN (SELECT value FROM json_each($2)) ORDER BY d.created_at LIMIT 1
