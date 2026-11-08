-- name: youtube_sync.store_listing.3
-- mode: write
-- scope: youtube_playlist $1
-- tables: youtube_playlist_items*
DELETE FROM youtube_playlist_items WHERE playlist_id = $1
