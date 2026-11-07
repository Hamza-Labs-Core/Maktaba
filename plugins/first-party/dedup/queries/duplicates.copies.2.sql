-- name: duplicates.copies.2
-- mode: read
-- scope: catalog
-- tables: video_copies, videos
SELECT c.video_id AS video_id, c.path AS path, c.size_bytes AS size_bytes FROM video_copies c JOIN videos v ON v.id = c.video_id WHERE 1 = 1 ORDER BY c.path
