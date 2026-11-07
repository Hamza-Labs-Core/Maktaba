-- name: copies.promote_copy.2
-- mode: read
-- scope: subject $1
-- tables: video_copies
SELECT path, size_bytes, mtime FROM video_copies WHERE video_id = $1 ORDER BY seen_at DESC, path
