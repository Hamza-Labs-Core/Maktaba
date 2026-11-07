-- name: topic_collections.plan_topics
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id FROM videos WHERE library_id = $1 AND deleted_at IS NULL
