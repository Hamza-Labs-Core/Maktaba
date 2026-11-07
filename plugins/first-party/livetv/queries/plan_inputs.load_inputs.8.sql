-- name: plan_inputs.load_inputs.8
-- mode: read
-- scope: catalog
-- tables: video_topic_tags
SELECT video_id, tag, score FROM video_topic_tags
