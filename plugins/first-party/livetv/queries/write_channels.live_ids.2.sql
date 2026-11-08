-- name: write_channels.live_ids.2
-- mode: read
-- scope: subject $1
-- tables: videos
SELECT id FROM videos WHERE id IN (SELECT value FROM json_each($1)) AND deleted_at IS NULL
