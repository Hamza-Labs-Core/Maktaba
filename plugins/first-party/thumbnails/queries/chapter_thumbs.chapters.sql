-- name: chapter_thumbs.chapters
-- mode: read
-- scope: subject $1
-- tables: chapters
SELECT seq, start_sec FROM chapters WHERE video_id = $1 ORDER BY seq
