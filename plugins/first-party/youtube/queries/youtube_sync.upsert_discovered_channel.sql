-- name: youtube_sync.upsert_discovered_channel
-- mode: write
-- scope: library $2
-- tables: youtube_channels*
INSERT INTO youtube_channels (channel_id, library_id, url, title, fetched_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (channel_id) DO UPDATE SET library_id = COALESCE(youtube_channels.library_id, EXCLUDED.library_id), title = COALESCE(youtube_channels.title, EXCLUDED.title)
