-- name: topic_recluster.run.2
-- mode: write
-- scope: library $1
-- tables: library_topics*
DELETE FROM library_topics WHERE library_id = $1
