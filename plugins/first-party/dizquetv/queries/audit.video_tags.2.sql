-- name: audit.video_tags.2
-- mode: read
-- scope: catalog
-- tables: video_topic_tags
SELECT video_id, tag, score FROM video_topic_tags WHERE video_id IN (SELECT value FROM json_each($1))
