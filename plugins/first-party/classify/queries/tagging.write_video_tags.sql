-- name: tagging.write_video_tags
-- mode: write
-- scope: subject $1
-- tables: video_topic_tags*
DELETE FROM video_topic_tags WHERE video_id = $1
