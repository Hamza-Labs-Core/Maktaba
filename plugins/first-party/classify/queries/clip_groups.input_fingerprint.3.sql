-- name: clip_groups.input_fingerprint.3
-- mode: read
-- scope: library $1
-- tables: video_topic_tags, videos
SELECT t.video_id AS v, t.tag AS t, t.score AS s FROM video_topic_tags t JOIN videos v ON v.id = t.video_id WHERE v.library_id = $1 ORDER BY 1, 2
