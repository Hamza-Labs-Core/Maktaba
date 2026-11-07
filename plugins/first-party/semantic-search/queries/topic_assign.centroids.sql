-- name: topic_assign.centroids
-- mode: read
-- scope: library $1
-- tables: library_topics
SELECT topic_id, centroid_vec FROM library_topics WHERE library_id = $1 AND model = $2 ORDER BY topic_id
