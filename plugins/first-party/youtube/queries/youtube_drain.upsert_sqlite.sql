-- name: youtube_drain.upsert_sqlite
-- mode: read
-- scope: library $1
-- tables: videos
SELECT id FROM videos WHERE library_id = $1 AND content_hash = $2
