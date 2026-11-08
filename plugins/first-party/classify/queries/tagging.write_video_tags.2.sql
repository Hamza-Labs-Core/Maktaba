-- name: tagging.write_video_tags.2
-- mode: write
-- scope: subject $1
-- tables: video_topic_tags*
INSERT INTO video_topic_tags (video_id, tag, score) VALUES ($1,$2,$3)
