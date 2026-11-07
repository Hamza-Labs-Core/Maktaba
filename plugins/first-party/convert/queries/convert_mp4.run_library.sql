-- name: convert_mp4.run_library
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id, path FROM videos WHERE library_id = $1 AND deleted_at IS NULL AND superseded_by IS NULL AND path IS NOT NULL ORDER BY path
