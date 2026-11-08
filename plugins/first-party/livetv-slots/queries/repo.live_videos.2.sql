-- name: repo.live_videos.2
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT id, title FROM videos WHERE id IN (SELECT value FROM json_each($1)) AND deleted_at IS NULL
