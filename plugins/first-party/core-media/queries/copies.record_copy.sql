-- name: copies.record_copy
-- mode: write
-- scope: subject $1
-- tables: video_copies*
INSERT INTO video_copies (video_id, path, size_bytes, mtime, seen_at) VALUES ($1, $2, $3, $4, $5) ON CONFLICT (video_id, path) DO UPDATE SET size_bytes = excluded.size_bytes, mtime = excluded.mtime, seen_at = excluded.seen_at
