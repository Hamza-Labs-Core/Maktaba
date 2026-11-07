-- name: youtube_sync.store_listing.4
-- mode: write
-- scope: catalog
-- tables: youtube_channels*
UPDATE youtube_channels SET status = 'ok', error = NULL, title = COALESCE($2, title), url = $3, fetched_at = $4 WHERE channel_id = $1
