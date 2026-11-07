-- name: index.videos
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id, path, duration_sec FROM videos WHERE library_id = $1 AND deleted_at IS NULL
