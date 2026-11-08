-- name: check_clips.load_clips.2
-- mode: read
-- scope: library $1
-- tables: video_topic_tags, videos
SELECT t.video_id AS v, t.tag AS tag, t.score AS score FROM video_topic_tags t JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1
