-- name: topic_assign.run
-- mode: write
-- scope: subject $1
-- tables: video_topics*
DELETE FROM video_topics WHERE video_id = $1
