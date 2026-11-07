-- name: stage.run
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id, title, size_bytes FROM videos WHERE library_id = $1 AND deleted_at IS NULL ORDER BY id
