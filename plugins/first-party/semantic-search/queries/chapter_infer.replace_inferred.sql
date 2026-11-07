-- name: chapter_infer.replace_inferred
-- mode: write
-- scope: subject $1
-- tables: chapters*
DELETE FROM chapters WHERE video_id = $1 AND source = 'inferred'
