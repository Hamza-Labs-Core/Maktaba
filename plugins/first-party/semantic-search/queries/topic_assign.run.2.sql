-- name: topic_assign.run.2
-- mode: write
-- scope: subject $1
-- tables: video_topics*
INSERT INTO video_topics (video_id, library_id, topic_id, score) VALUES ($1, $2, $3, $4)
