-- name: content_hash.run.2
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id FROM videos WHERE library_id = $1 AND content_hash = $2 AND id <> $3
