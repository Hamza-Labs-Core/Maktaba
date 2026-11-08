-- name: topic_collections.run.3
-- mode: write
-- scope: topic_collection $1, subject $2
-- tables: topic_items*
INSERT INTO topic_items (topic_id, video_id, position) VALUES ($1,$2,$3)
