-- name: topic_recluster.input_fingerprint
-- mode: read
-- scope: library $1
-- tables: libraries
SELECT 1 FROM libraries WHERE id = $1
