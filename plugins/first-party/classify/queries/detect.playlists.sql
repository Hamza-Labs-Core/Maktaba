-- name: detect.playlists
-- mode: read
-- scope: library $1
-- tables: media_metadata_enrichment, videos, youtube_playlist_items, youtube_playlists
SELECT p.playlist_id AS pid, p.title AS title, m.video_id AS vid, i.position AS pos FROM youtube_playlists p JOIN youtube_playlist_items i ON i.playlist_id = p.playlist_id JOIN media_metadata_enrichment m ON m.external_id = i.youtube_id AND m.provider = 'youtube' JOIN videos v ON v.id = m.video_id WHERE p.is_real = $2 AND v.library_id = $1 AND v.deleted_at IS NULL ORDER BY p.playlist_id, i.position
