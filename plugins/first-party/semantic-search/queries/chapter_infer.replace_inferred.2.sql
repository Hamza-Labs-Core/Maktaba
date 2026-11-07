-- name: chapter_infer.replace_inferred.2
-- mode: write
-- scope: subject $1
-- tables: chapters*
INSERT INTO chapters (video_id, seq, start_sec, end_sec, title, source, metadata) VALUES ($1, $2, $3, $4, $5, 'inferred', $6)
