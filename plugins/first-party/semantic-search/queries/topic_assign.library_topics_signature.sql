-- name: topic_assign.library_topics_signature
-- mode: read
-- scope: library $1
-- tables: library_topics
SELECT COUNT(*) AS n, MAX(computed_at) AS at FROM library_topics WHERE library_id = $1
