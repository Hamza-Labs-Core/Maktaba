-- name: youtube_drain.roots
-- mode: read
-- scope: library $1
-- tables: libraries
SELECT roots FROM libraries WHERE id = $1
