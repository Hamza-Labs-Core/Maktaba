-- name: detect.input_fingerprint
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id, title, filename FROM videos WHERE library_id = $1 AND deleted_at IS NULL ORDER BY id
