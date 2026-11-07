-- name: groups.members.2
-- mode: read
-- scope: catalog
-- tables: clip_group_items, videos
SELECT v.id AS id, v.path AS path, COALESCE(v.title, v.filename) AS title FROM clip_group_items t JOIN videos v ON v.id = t.video_id WHERE t.group_id = $1 AND v.deleted_at IS NULL ORDER BY t.position
