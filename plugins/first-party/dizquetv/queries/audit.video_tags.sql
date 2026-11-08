-- name: audit.video_tags
-- mode: read
-- scope: catalog
-- tables: video_topic_tags
SELECT video_id, tag, score FROM video_topic_tags WHERE video_id = ANY($1::uuid[])
