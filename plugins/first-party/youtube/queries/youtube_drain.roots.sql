-- name: youtube_drain.roots
-- mode: read
-- scope: catalog
-- tables: libraries
SELECT roots FROM libraries WHERE id = $1
