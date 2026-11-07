-- name: link.upsert_youtube_video.3
-- mode: write
-- scope: catalog
-- tables: youtube_playlist_items*
INSERT INTO youtube_playlist_items (playlist_id, youtube_id, position) VALUES ($1,$2,$3) ON CONFLICT (playlist_id, youtube_id) DO UPDATE SET position = COALESCE(EXCLUDED.position, youtube_playlist_items.position)
