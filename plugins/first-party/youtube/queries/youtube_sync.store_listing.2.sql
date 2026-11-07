-- name: youtube_sync.store_listing.2
-- mode: write
-- scope: catalog
-- tables: youtube_playlists*
INSERT INTO youtube_playlists (playlist_id, channel_id, title, item_count, is_real, source, fetched_at) VALUES ($1,$2,$3,$4,$5,'youtube',$6) ON CONFLICT (playlist_id) DO UPDATE SET title = EXCLUDED.title, item_count = EXCLUDED.item_count, source = 'youtube', channel_id = COALESCE(youtube_playlists.channel_id, EXCLUDED.channel_id)
