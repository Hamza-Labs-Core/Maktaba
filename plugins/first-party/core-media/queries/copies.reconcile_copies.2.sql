-- name: copies.reconcile_copies.2
-- mode: read
-- scope: library $1
-- tables: video_copies, videos
SELECT c.video_id AS video_id, c.path AS path, v.path AS own, v.state AS state FROM video_copies c JOIN videos v ON v.id = c.video_id WHERE v.library_id = $1 ORDER BY c.video_id
