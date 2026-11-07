-- name: link.upsert_youtube_video.2
-- mode: write
-- scope: catalog
-- tables: youtube_playlists*
INSERT INTO youtube_playlists (playlist_id, channel_id, title, is_real, source, fetched_at) VALUES ($1,$2,$3,$4,'info_json',$5) ON CONFLICT (playlist_id) DO UPDATE SET title = EXCLUDED.title
