-- name: repo.hand_items
-- mode: read
-- scope: catalog
-- tables: videos
SELECT duration_sec FROM videos WHERE id = $1
