-- name: store.find_video_by_path
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id, size_bytes, mtime, content_hash
  FROM videos
 WHERE library_id = $1
   AND path       = $2
 LIMIT 1
