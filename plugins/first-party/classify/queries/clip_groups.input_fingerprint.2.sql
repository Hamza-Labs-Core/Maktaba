-- name: clip_groups.input_fingerprint.2
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id, size_bytes, mtime FROM videos WHERE library_id = $1 AND deleted_at IS NULL ORDER BY id
