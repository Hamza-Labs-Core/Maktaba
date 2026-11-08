-- name: youtube_sync.upsert_sidecar_playlist
-- mode: write
-- scope: catalog
-- tables: youtube_playlists*
INSERT INTO youtube_playlists (playlist_id, channel_id, title, item_count, is_real, source, fetched_at) VALUES ($1,$2,$3,$4,$5,'info_json',$6) ON CONFLICT (playlist_id) DO UPDATE SET title = EXCLUDED.title, item_count = COALESCE(EXCLUDED.item_count, youtube_playlists.item_count), channel_id = COALESCE(youtube_playlists.channel_id, EXCLUDED.channel_id)
