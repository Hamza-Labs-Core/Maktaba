-- name: clip_groups.input_fingerprint.4
-- mode: read
-- scope: library $1
-- tables: topic_collections
SELECT playlist_id FROM topic_collections WHERE library_id = $1 ORDER BY 1
