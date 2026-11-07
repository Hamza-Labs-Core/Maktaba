-- name: youtube_derive.load_evidence.2
-- mode: read
-- scope: catalog
-- tables: media_metadata_enrichment, youtube_playlist_items, youtube_playlists
SELECT i.youtube_id AS yid, p.playlist_id AS pid, p.title AS title, p.channel_id AS cid FROM youtube_playlist_items i JOIN youtube_playlists p ON p.playlist_id = i.playlist_id WHERE p.is_real AND i.youtube_id IN (SELECT m.external_id FROM  media_metadata_enrichment m WHERE m.provider = 'youtube')
