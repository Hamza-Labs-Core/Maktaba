-- name: repo.hand_items
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT duration_sec FROM videos WHERE id = $1
