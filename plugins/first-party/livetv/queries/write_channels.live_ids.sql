-- name: write_channels.live_ids
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT id FROM videos WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL
