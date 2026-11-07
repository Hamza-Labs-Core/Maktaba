-- name: topic_recluster.run.3
-- mode: write
-- scope: library $1
-- tables: library_topics*
INSERT INTO library_topics (library_id, topic_id, label, centroid_vec, video_count, computed_at, model) VALUES ($1, $2, $3, $4, $5, $6, $7)
