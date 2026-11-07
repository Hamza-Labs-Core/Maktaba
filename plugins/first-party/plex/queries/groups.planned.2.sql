-- name: groups.planned.2
-- mode: read
-- scope: library $1
-- tables: topic_collections
SELECT id, name FROM topic_collections WHERE library_id = $1 ORDER BY name, id
