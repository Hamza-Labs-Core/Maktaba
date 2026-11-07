-- name: topic_collections.run.3
-- mode: write
-- scope: subject $2
-- tables: topic_items*
INSERT INTO topic_items (topic_id, video_id, position) VALUES ($1,$2,$3)
