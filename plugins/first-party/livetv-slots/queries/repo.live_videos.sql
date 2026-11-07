-- name: repo.live_videos
-- mode: read
-- scope: catalog
-- tables: videos
SELECT id, title FROM videos WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL
