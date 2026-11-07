-- name: chapter_infer.authored
-- mode: read
-- scope: subject $1
-- tables: chapters
SELECT COUNT(*) FROM chapters WHERE video_id = $1 AND source IN ('embedded', 'manual')
