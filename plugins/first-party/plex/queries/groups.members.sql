-- name: groups.members
-- mode: read
-- scope: catalog
-- tables: topic_items, videos
SELECT v.id AS id, v.path AS path, COALESCE(v.title, v.filename) AS title FROM topic_items t JOIN videos v ON v.id = t.video_id WHERE t.topic_id = $1 AND v.deleted_at IS NULL ORDER BY t.position
