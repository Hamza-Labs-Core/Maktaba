-- name: tagging.read_video_tags
-- mode: read
-- scope: subject $1
-- tables: video_topic_tags
SELECT tag, score FROM video_topic_tags WHERE video_id = $1
