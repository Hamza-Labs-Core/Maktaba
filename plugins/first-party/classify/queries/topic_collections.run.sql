-- name: topic_collections.run
-- mode: write
-- scope: library $1
-- tables: topic_collections*
DELETE FROM topic_collections WHERE library_id = $1
