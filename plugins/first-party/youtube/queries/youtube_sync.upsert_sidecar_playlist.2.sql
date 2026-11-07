-- name: youtube_sync.upsert_sidecar_playlist.2
-- mode: write
-- scope: catalog
-- tables: youtube_playlist_items*
INSERT INTO youtube_playlist_items (playlist_id, youtube_id, position) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING
