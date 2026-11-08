-- name: store.save_candidate.6
-- mode: write
-- scope: library $3
-- tables: videos, video_copies*
DELETE FROM video_copies
 WHERE path = $1 AND video_id <> $2
   AND video_id IN (SELECT id FROM videos WHERE library_id = $3)
