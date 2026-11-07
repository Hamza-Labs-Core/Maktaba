-- name: subtitle_gen.run.2
-- mode: write
-- scope: subject $1
-- tables: subtitle_files*
UPDATE subtitle_files SET deleted_at = $4 WHERE video_id = $1 AND language = $2 AND format = $3 AND source = 'generated' AND deleted_at IS NULL
