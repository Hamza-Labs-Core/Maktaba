-- name: common.load_video
-- mode: read
-- scope: catalog
-- tables: videos
SELECT id, library_id, path, filename, title, size_bytes, mtime, duration_sec, poster_path, metadata FROM videos WHERE id = $1
