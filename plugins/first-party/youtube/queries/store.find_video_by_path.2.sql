-- name: store.find_video_by_path.2
-- mode: read
-- scope: library $1
-- tables: video_copies, videos
SELECT c.video_id AS id, c.size_bytes AS size_bytes, c.mtime AS mtime,
       v.content_hash AS content_hash
  FROM video_copies c
  JOIN videos v ON v.id = c.video_id
 WHERE v.library_id = $1
   AND c.path       = $2
   AND c.size_bytes = v.size_bytes
 LIMIT 1
