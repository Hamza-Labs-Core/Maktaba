-- name: duplicates.copies
-- mode: read
-- scope: library $1
-- tables: video_copies, videos
SELECT c.video_id AS video_id, c.path AS path, c.size_bytes AS size_bytes FROM video_copies c JOIN videos v ON v.id = c.video_id WHERE v.library_id = $1 ORDER BY c.path
