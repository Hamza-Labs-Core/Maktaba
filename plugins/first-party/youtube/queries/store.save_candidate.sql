-- name: store.save_candidate
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id, path FROM videos
 WHERE library_id = $1
   AND content_hash = $2
 LIMIT 1
