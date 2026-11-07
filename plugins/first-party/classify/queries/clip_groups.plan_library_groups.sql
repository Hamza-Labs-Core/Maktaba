-- name: clip_groups.plan_library_groups
-- mode: read
-- scope: library $1
-- tables: topic_collections
SELECT playlist_id FROM topic_collections WHERE library_id = $1
