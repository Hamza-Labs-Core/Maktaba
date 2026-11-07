-- name: youtube_sync.set_channel_status
-- mode: write
-- scope: catalog
-- tables: youtube_channels*
UPDATE youtube_channels SET status = $2, error = $3, title = COALESCE($4, title), fetched_at = $5 WHERE channel_id = $1
