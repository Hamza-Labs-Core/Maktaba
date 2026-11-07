-- name: subtitle_gen.run
-- mode: read
-- scope: subject $1
-- tables: subtitle_files
SELECT sha256 FROM subtitle_files WHERE video_id = $1 AND language = $2 AND format = $3 AND source = 'generated' AND deleted_at IS NULL
